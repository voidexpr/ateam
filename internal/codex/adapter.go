// Package codex contains Codex CLI adapter details that are specific to the
// interactive TUI.
package codex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/tmuxctl"
)

const (
	DefaultStartTimeout     = 15 * time.Second
	DefaultBusyTimeout      = 5 * time.Minute
	DefaultQuiescenceWindow = 2 * time.Second
	pollInterval            = 250 * time.Millisecond
	updateCheckConfigKey    = "check_for_update_on_startup"
	// submitDebounce is the wait between input delivery (TypeLiteral or
	// SendLiteral) and the Enter keystroke that submits. Mirrors
	// gastown's SendKeysDebounced default — protects against the paste
	// or typed keystrokes still being in-flight when Enter arrives.
	submitDebounce = 100 * time.Millisecond
	// idleConsecutiveMatches is how many polls in a row must show the idle
	// prompt (and no busy indicator) before we declare the agent done. Codex's
	// TUI redraws a prompt-shaped line in the inter-tool gap (~500ms) while
	// still working; a single match is unreliable.
	idleConsecutiveMatches = 2
)

var unattendedDisabledFeatures = []string{"apps", "plugins"}

// codexBusyMarkers are case-insensitive substrings that, if present anywhere in
// the captured pane, mean Codex is actively working. Idle detection short-
// circuits when any of these is found regardless of prompt shape.
var codexBusyMarkers = []string{
	"esc to interrupt",
	"esc to cancel",
	"thinking…",
	"working…",
	"streaming…",
}

type IdleSignal string

const (
	IdleSignalPromptMatch IdleSignal = "prompt_match"
	IdleSignalQuiescence  IdleSignal = "quiescence"
	IdleSignalTimeout     IdleSignal = "timeout"
)

// TmuxConfig configures one Codex TUI invocation.
type TmuxConfig struct {
	Command          string
	Args             []string
	ExtraArgs        []string
	Model            string
	Effort           string
	InitialInput     string
	WorkDir          string
	Env              map[string]string
	Width            int
	Height           int
	StartTimeout     time.Duration
	BusyTimeout      time.Duration
	QuiescenceWindow time.Duration
	CmdFactory       tmuxctl.CmdFactory
	// SocketPath is the absolute path of the tmux unix socket. Required.
	// Caller derives it from <ProjectDir>/.ateam/cache/tmux/exec-<EXEC_ID>.sock.
	SocketPath string
	// SessionName is the tmux session name. Required. Caller derives from EXEC_ID.
	SessionName string
	// ExecID is the calldb row ID for this run. Used to derive a stable
	// sentinel that gets embedded in free-form prompts so the same run's
	// rollout JSONL can be reliably identified — even when a concurrent
	// codex-tmux run in the same workdir would otherwise be ambiguous.
	// Zero = no marker (falls back to cwd+timestamp matching, the v1.1
	// behavior; OK for slash commands where we can't safely inject text).
	ExecID int64
	// OnPanePID, if non-nil, is invoked once with the pane's foreground PID
	// after the codex TUI is ready. The codex-tmux agent uses this to emit
	// a `system` event so the runner can record the real PID in agent_execs.
	OnPanePID func(pid int)
	// OnHeartbeat, if non-nil, is invoked whenever the pane content changes
	// during BUSY, rate-limited by HeartbeatInterval. The text is a short
	// preview of the latest tail of the pane. The codex-tmux agent surfaces
	// these as `thinking` events so the runner's stall watchdog stays alive
	// during long `/review` runs that emit no other events.
	OnHeartbeat func(preview string)
	// HeartbeatInterval throttles OnHeartbeat. Zero = default 8s. The pane
	// changes constantly during a Codex turn (cursor, status bar) so an
	// uncapped heartbeat would spam the stream.
	HeartbeatInterval time.Duration
	// TmuxLogPath, when non-empty, enables a per-run JSONL trace of every
	// tmux interaction (sends, captures-with-hash-dedup, waitReady /
	// waitIdle decisions, errors). Eager-flushed so `tail -f` works on a
	// stuck run. The codex-tmux agent points this at
	// `.ateam/logs/<EXEC_ID>/tmux.log` next to the other per-exec logs.
	TmuxLogPath string
}

// DefaultHeartbeatInterval is the throttle floor for OnHeartbeat callbacks.
const DefaultHeartbeatInterval = 8 * time.Second

type TmuxResult struct {
	Output      string
	RawCapture  string
	SessionName string
	IdleSignal  IdleSignal
	Duration    time.Duration
	// PanePID is the foreground process PID running in the tmux pane after
	// startup. 0 means it was never resolved (e.g. start failed before
	// PromptReady).
	PanePID int
	// SessionStats are the token/cost stats mined from Codex's rollout JSONL
	// at `~/.codex/sessions/<date>/rollout-*.jsonl`. Populated after waitIdle
	// returns; zero value means the log wasn't found or hadn't been written
	// yet. Always best-effort: the run is not failed when this is empty.
	SessionStats SessionStats
}

type ExtractedOutput struct {
	Output           string
	CommandEchoFound bool
}

// promptDelivery describes how a prompt is shipped to Codex. Slash commands
// must be sent as-is (the slash parser only triggers on a single-line input
// starting with `/`); free-form prompts get a sentinel suffix so multi-line
// echoes can be located reliably in the pane capture.
type promptDelivery struct {
	SentText       string // what we type into the TUI
	EchoMarker     string // line we look for to confirm Codex echoed the prompt
	IsSlashCommand bool
	// FollowupSteps are tmux key sequences sent after the initial input has
	// been submitted and waitIdle returns. Used to navigate interactive
	// submenus that a slash command may open (e.g. `/review` shows a
	// preset picker before running). Each non-empty element is one
	// `send-keys` call with whitespace-split key names; the user can mix
	// literal characters and named keys: `2 Enter`, `Down Down Enter`,
	// `Tab` etc. Empty when the prompt has no follow-up lines.
	FollowupSteps [][]string
}

// preparePrompt returns the text to type plus the echo marker to look for.
//
// Three shapes:
//
//  1. Slash command, single line — e.g. `/review the pending changes`.
//     Sent verbatim so codex's slash parser fires. EchoMarker is the
//     prompt itself. No follow-up steps.
//
//  2. Slash command with follow-up lines — e.g. `/review\n2 Enter`. The
//     first line is the slash command (sent verbatim); subsequent
//     non-empty lines are tmux key sequences to apply after the initial
//     submit settles. Used to navigate interactive submenus (codex's
//     bare `/review` opens a preset picker). Each follow-up line is
//     whitespace-split into tmux key names (`Down`, `Enter`, `Tab`, or
//     bare characters); empty lines are ignored.
//
//  3. Free-form (default) — anything else. A sentinel is appended on
//     its own line; the sentinel becomes the echo marker. Multi-line
//     content ships via paste-buffer (bracketed paste) so embedded
//     newlines stay as input newlines rather than premature submits.
//     When execID > 0 the sentinel is deterministic
//     (`[ateam-exec-<id>]`) — codex echoes it into the rollout JSONL's
//     user_message, letting FindSessionLog disambiguate concurrent
//     codex-tmux runs in the same workdir. execID == 0 (unit tests)
//     falls back to a random sentinel.
func preparePrompt(prompt string, execID int64) promptDelivery {
	trimmed := strings.TrimSpace(prompt)
	if strings.HasPrefix(trimmed, "/") {
		// Slash command — split off any follow-up lines as menu steps.
		lines := strings.Split(prompt, "\n")
		firstLine := strings.TrimRight(lines[0], "\r")
		first := strings.TrimSpace(firstLine)
		if first == "" || !strings.HasPrefix(first, "/") {
			// Defensive: leading blank or whitespace pushed the slash
			// to a later line. Treat as free-form to avoid surprises.
		} else {
			var steps [][]string
			for _, line := range lines[1:] {
				s := strings.TrimSpace(line)
				if s == "" {
					continue
				}
				steps = append(steps, strings.Fields(s))
			}
			return promptDelivery{
				SentText:       first,
				EchoMarker:     first,
				IsSlashCommand: true,
				FollowupSteps:  steps,
			}
		}
	}
	sentinel := newSentinel(execID)
	return promptDelivery{
		SentText:   prompt + "\n" + sentinel,
		EchoMarker: sentinel,
	}
}

// SessionLogMarker returns the marker string preparePrompt injects into
// free-form prompts for a given EXEC_ID. Exposed so FindSessionLog can match
// rollout files by user_message content.
func SessionLogMarker(execID int64) string {
	if execID <= 0 {
		return ""
	}
	return fmt.Sprintf("[ateam-exec-%d]", execID)
}

func newSentinel(execID int64) string {
	if execID > 0 {
		return SessionLogMarker(execID)
	}
	var buf [10]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// rand.Read effectively never fails on linux/darwin; fall back to
		// time-based uniqueness instead of crashing the run.
		return fmt.Sprintf("[ateam-end-%016x]", time.Now().UnixNano())
	}
	return "[ateam-end-" + hex.EncodeToString(buf[:]) + "]"
}

// RunTmux starts Codex in a tmux session, sends the configured initial input,
// waits until Codex is idle again, captures the pane, and kills the session.
func RunTmux(ctx context.Context, cfg TmuxConfig) (TmuxResult, error) {
	started := time.Now()
	if cfg.Command == "" {
		cfg.Command = "codex"
	}
	if cfg.InitialInput == "" {
		return TmuxResult{Duration: time.Since(started)}, fmt.Errorf("codex-tmux initial input is required")
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.BusyTimeout == 0 {
		cfg.BusyTimeout = effectiveBusyTimeout(ctx)
	}
	if cfg.QuiescenceWindow == 0 {
		cfg.QuiescenceWindow = DefaultQuiescenceWindow
	}
	if cfg.Width == 0 {
		cfg.Width = tmuxctl.DefaultWidth
	}
	if cfg.Height == 0 {
		cfg.Height = tmuxctl.DefaultHeight
	}

	if cfg.SessionName == "" {
		return TmuxResult{Duration: time.Since(started)}, fmt.Errorf("codex-tmux: SessionName is required")
	}
	if cfg.SocketPath == "" {
		return TmuxResult{SessionName: cfg.SessionName, Duration: time.Since(started)}, fmt.Errorf("codex-tmux: SocketPath is required")
	}
	sessionName := cfg.SessionName
	args := InteractiveArgs(cfg.Args, cfg.Model, cfg.Effort, cfg.ExtraArgs)
	cmd := append([]string{cfg.Command}, args...)

	tracer, traceCloser, _ := newTmuxTracer(cfg.TmuxLogPath, started)
	if traceCloser != nil {
		defer traceCloser.Close()
	}
	tracer.event("start", map[string]any{
		"session":    sessionName,
		"socket":     cfg.SocketPath,
		"command":    strings.Join(cmd, " "),
		"model":      cfg.Model,
		"effort":     cfg.Effort,
		"exec_id":    cfg.ExecID,
		"workdir":    cfg.WorkDir,
		"prompt_len": len(cfg.InitialInput),
		"prompt":     cfg.InitialInput,
	})

	sess, err := tmuxctl.New(ctx, cfg.SocketPath, sessionName, cfg.Width, cfg.Height, envList(cfg.Env), cfg.WorkDir, cmd, cfg.CmdFactory)
	if err != nil {
		tracer.event("error", map[string]any{"stage": "tmuxctl.New", "err": err.Error()})
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}
	// Always tear down the session, including on ctx cancellation. The
	// exec.CommandContext + Setpgid combo only signals the tmux server's
	// process group; the codex process inside the pane is in a *different*
	// process group (tmux puts each pane in its own pgrp) and may survive
	// the cascade. sess.Kill explicitly issues kill-session, then removes
	// the per-run socket and bootstrap script — without it a canceled run
	// leaks artifacts under .ateam/cache/tmux/. Use a fresh context so
	// the cleanup itself isn't poisoned by the canceled run ctx.
	defer func() {
		_ = sess.Kill(context.Background())
	}()

	ready, err := waitReady(ctx, sess, cfg.StartTimeout, tracer)
	if err != nil {
		raw, _ := sess.Capture(context.Background(), true)
		tracer.event("error", map[string]any{"stage": "waitReady", "err": err.Error()})
		return TmuxResult{RawCapture: raw, SessionName: sessionName, Duration: time.Since(started)}, err
	}
	tracer.event("waitReady.ok", map[string]any{})

	// Once codex is past its onboarding/dialogs we can ask tmux for the
	// pane's foreground PID — that's the codex process we want recorded in
	// agent_execs.pid for the runner's liveness probe.
	panePID, _ := sess.PanePID(ctx)
	if panePID > 0 && cfg.OnPanePID != nil {
		cfg.OnPanePID(panePID)
	}
	tracer.event("pane_pid", map[string]any{"pid": panePID})

	delivery := preparePrompt(cfg.InitialInput, cfg.ExecID)
	// Slash commands must be TYPED char-by-char (send-keys -l) so codex's TUI
	// slash-command parser sees the `/` as a keystroke and enters slash mode.
	// Pasting "/review" via bracketed paste arrives as bulk content, codex
	// treats it as a regular message, and the subsequent C-Enter doesn't
	// submit cleanly. Free-form prompts (which we wrap with a sentinel and
	// therefore always carry a newline) need paste-buffer to preserve the
	// embedded newline through bracketed paste — `send-keys -l` would
	// transmit `\n` as an Enter keystroke and submit the partial prompt.
	var sendErr error
	if delivery.IsSlashCommand {
		tracer.event("type-literal", map[string]any{"text": delivery.SentText, "slash": true})
		sendErr = sess.TypeLiteral(ctx, delivery.SentText)
	} else {
		tracer.event("send-literal", map[string]any{"text": delivery.SentText, "marker": delivery.EchoMarker})
		sendErr = sess.SendLiteral(ctx, delivery.SentText)
	}
	if sendErr != nil {
		tracer.event("error", map[string]any{"stage": "send-prompt", "err": sendErr.Error()})
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, sendErr
	}
	if err := waitInputEcho(ctx, sess, delivery.EchoMarker, delivery.IsSlashCommand, 2*time.Second); err != nil {
		tracer.event("error", map[string]any{"stage": "waitInputEcho", "err": err.Error()})
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}
	// Debounce between input delivery and the submit key. gastown's
	// SendKeysDebounced uses 100ms for the same reason: paste/type can be
	// in-flight to the TUI's input buffer when Enter arrives and the submit
	// gets dropped or mis-targeted.
	if err := sleepShort(ctx, submitDebounce); err != nil {
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}
	// Enter (not C-Enter): codex v0.132+ binds Enter as submit. C-Enter
	// either no-ops or inserts a newline depending on terminal capability
	// flags (we observed v0.132.0 treating C-Enter as newline-insert on
	// single-line `/review`, leaving the prompt unsubmitted).
	tracer.event("send-keys", map[string]any{"keys": []string{"Enter"}, "stage": "submit"})
	if err := sess.SendKeys(ctx, "Enter"); err != nil {
		tracer.event("error", map[string]any{"stage": "submit", "err": err.Error()})
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}

	hbInterval := cfg.HeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = DefaultHeartbeatInterval
	}
	idleSignal, err := waitIdle(ctx, sess, ready, cfg.BusyTimeout, cfg.QuiescenceWindow, cfg.OnHeartbeat, hbInterval, tracer)
	tracer.event("waitIdle.return", map[string]any{"signal": string(idleSignal), "err_present": err != nil})

	// Multi-line slash command: navigate any interactive submenu codex
	// opened by sending the follow-up key sequences. Each step is
	// re-captured-then-waited so codex has a chance to react before the
	// next step fires. waitIdle's `initial` argument is the pre-step
	// pane so its quiescence check waits for an actual change.
	if err == nil {
		for i, step := range delivery.FollowupSteps {
			if len(step) == 0 {
				continue
			}
			pre, perr := sess.Capture(ctx, true)
			if perr != nil {
				err = perr
				break
			}
			if perr := sleepShort(ctx, submitDebounce); perr != nil {
				err = perr
				break
			}
			tracer.event("send-keys", map[string]any{"keys": step, "stage": "followup", "index": i})
			if perr := sess.SendKeys(ctx, step...); perr != nil {
				err = perr
				break
			}
			var iderr error
			idleSignal, iderr = waitIdle(ctx, sess, pre, cfg.BusyTimeout, cfg.QuiescenceWindow, cfg.OnHeartbeat, hbInterval, tracer)
			tracer.event("waitIdle.return", map[string]any{"signal": string(idleSignal), "err_present": iderr != nil, "stage": "followup", "index": i})
			if iderr != nil {
				err = iderr
				break
			}
		}
	}

	raw, capErr := sess.Capture(context.Background(), true)
	if capErr != nil && err == nil {
		err = capErr
	}
	extracted := ExtractCommandOutputWithStatus(raw, delivery.EchoMarker, delivery.IsSlashCommand)
	out := extracted.Output
	result := TmuxResult{
		Output:      out,
		RawCapture:  raw,
		SessionName: sessionName,
		IdleSignal:  idleSignal,
		Duration:    time.Since(started),
		PanePID:     panePID,
	}
	// Best-effort: mine the Codex rollout JSONL for token/cost. Never fails
	// the run — the pane output we already have is the source of truth for
	// success/failure; this just enriches the metrics. When the prompt was
	// free-form, we passed an EXEC_ID-tagged sentinel through preparePrompt;
	// FindSessionLog uses that to disambiguate concurrent runs in the same
	// workdir. Slash commands have no marker (we can't safely inject text)
	// and fall back to cwd+timestamp matching.
	marker := ""
	if !delivery.IsSlashCommand {
		marker = SessionLogMarker(cfg.ExecID)
	}
	if logPath, ferr := FindSessionLog("", cfg.WorkDir, started, marker); ferr == nil && logPath != "" {
		if stats, rerr := ReadSessionStats(logPath); rerr == nil {
			result.SessionStats = stats
		}
	}
	if err != nil {
		return result, err
	}
	if err := validateCommandOutput(cfg.InitialInput, extracted); err != nil {
		return result, err
	}
	return result, nil
}

func waitReady(ctx context.Context, sess *tmuxctl.Session, timeout time.Duration, tracer *tmuxTracer) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rendered, err := sess.Capture(ctx, true)
		if err != nil {
			return "", err
		}
		tracer.capture(rendered, PromptReady(rendered), CodexBusy(rendered))
		if PromptReady(rendered) {
			return rendered, nil
		}
		if tmuxCommandExited(rendered) {
			return rendered, fmt.Errorf("codex TUI exited before becoming ready")
		}
		if dismissed, err := dismissStartupDialog(ctx, sess, rendered); err != nil {
			return rendered, err
		} else if dismissed {
			if err := sleepPoll(ctx); err != nil {
				return rendered, err
			}
			continue
		}
		if time.Now().After(deadline) {
			return rendered, fmt.Errorf("codex TUI did not become ready within %s; last_capture_len=%d tail=%q", timeout, len(rendered), captureTail(rendered, 400))
		}
		if err := sleepPoll(ctx); err != nil {
			return rendered, err
		}
	}
}

func captureTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// waitInputEcho polls until the echo marker appears in the input-box area of
// the pane.
//
// For slash commands the match requires the codex `› ` prompt prefix on the
// line so banner text like `model: gpt-5.5 xhigh /model to change` can't
// false-positive a `/model` slash command before the user input has actually
// been typed. For free-form prompts the marker is a per-run sentinel that's
// unique enough that contamination from chrome is not possible — that path
// uses a plain substring match.
func waitInputEcho(ctx context.Context, sess *tmuxctl.Session, marker string, isSlashCommand bool, timeout time.Duration) error {
	if marker == "" {
		return fmt.Errorf("codex-tmux: internal error — empty echo marker")
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rendered, err := sess.Capture(ctx, true)
		if err != nil {
			return err
		}
		if echoVisible(rendered, marker, isSlashCommand) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("codex TUI did not echo input marker %q within %s", marker, timeout)
		}
		if err := sleepPoll(ctx); err != nil {
			return err
		}
	}
}

// echoVisible reports whether the marker is present on the bottom-of-pane
// input region. Slash commands require the `› ` prefix so a slash command
// name that appears as a substring of codex's banner ("…  /model to change…")
// cannot false-match before the user input is actually echoed. Free-form
// prompts (sentinel marker) accept any substring match.
func echoVisible(rendered, marker string, isSlashCommand bool) bool {
	for _, line := range nonEmptyTail(rendered, 20) {
		normalized := normalizeLineForMatch(line)
		if isSlashCommand {
			if strings.HasPrefix(strings.TrimSpace(normalized), "› "+marker) {
				return true
			}
			continue
		}
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func waitIdle(ctx context.Context, sess *tmuxctl.Session, initial string, timeout, quiescence time.Duration, onHeartbeat func(string), hbInterval time.Duration, tracer *tmuxTracer) (IdleSignal, error) {
	deadline := time.Now().Add(timeout)
	initialHash := stableHash(initial)
	lastHash := initialHash
	lastChanged := time.Now()
	promptWasReady := true
	consecutiveReady := 0
	consecutiveQuiet := 0
	lastHeartbeat := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return IdleSignalTimeout, err
		}
		rendered, err := sess.Capture(ctx, true)
		if err != nil {
			return IdleSignalTimeout, err
		}
		hash := stableHash(rendered)
		if hash != lastHash {
			lastHash = hash
			lastChanged = time.Now()
			// Heartbeat on pane change. Rate-limited so a Codex turn that
			// continuously redraws (e.g. spinner + tool output) doesn't
			// flood the stream — the runner only needs a periodic signal
			// to keep its stall watchdog alive.
			if onHeartbeat != nil && time.Since(lastHeartbeat) >= hbInterval {
				onHeartbeat(heartbeatPreview(rendered))
				lastHeartbeat = time.Now()
			}
		}

		busy := CodexBusy(rendered)
		ready := PromptReady(rendered)
		tracer.capture(rendered, ready, busy)

		// Busy indicator overrides any prompt-match: Codex's TUI redraws a
		// prompt-shaped line during inter-tool gaps while still working.
		if busy {
			promptWasReady = false
			consecutiveReady = 0
			consecutiveQuiet = 0
		} else {
			if ready && !promptWasReady {
				consecutiveReady++
				if consecutiveReady >= idleConsecutiveMatches {
					return IdleSignalPromptMatch, nil
				}
			} else if !ready {
				promptWasReady = false
				consecutiveReady = 0
			}
		}

		// Quiescence fallback: pane hash unchanged for `quiescence` AND we are
		// not currently busy. Require two consecutive quiet observations so a
		// single capture that happens to repeat the previous capture's bytes
		// can't masquerade as completion. Reset the counter on *any* failure
		// of the quiescence condition (busy OR fresh pane change OR
		// hash-still-equals-initial) so a pane that flickers between idle and
		// active doesn't accumulate spurious "quiet" credits.
		quiescent := !busy && hash != initialHash && time.Since(lastChanged) >= quiescence
		if quiescent {
			consecutiveQuiet++
			if consecutiveQuiet >= idleConsecutiveMatches {
				return IdleSignalQuiescence, nil
			}
		} else {
			consecutiveQuiet = 0
		}
		if time.Now().After(deadline) {
			_ = sess.SendKeys(context.Background(), "C-c")
			return IdleSignalTimeout, fmt.Errorf("codex TUI did not become idle within %s", timeout)
		}
		if err := sleepPoll(ctx); err != nil {
			return IdleSignalTimeout, err
		}
	}
}

// heartbeatPreview extracts a short preview of the latest pane state, suitable
// for a `thinking` StreamEvent. Returns the last non-empty rendered line
// truncated to ~80 chars. Empty input produces the literal "(pane updated)" so
// downstream consumers always see *some* heartbeat text.
func heartbeatPreview(rendered string) string {
	lines := nonEmptyTail(rendered, 1)
	if len(lines) == 0 {
		return "(pane updated)"
	}
	preview := strings.TrimSpace(lines[0])
	if len(preview) > 80 {
		preview = preview[:80] + "…"
	}
	if preview == "" {
		return "(pane updated)"
	}
	return preview
}

// CodexBusy reports whether the rendered pane shows Codex actively working.
// Used by waitIdle to suppress false-positive completions during inter-tool
// gaps where a prompt-shaped line briefly appears.
//
// CRITICAL: scope the scan to the bottom of the pane (busyScanLines tail).
// `capture-pane -S -` returns the full scrollback, which preserves every
// historical busy phase. A naive strings.Contains over the whole capture
// matches "Esc to interrupt" left over from a tool call that finished 10
// minutes ago and never clears — that's the bug that caused a 20m
// busy_timeout on a /review run that actually completed at 12m.
func CodexBusy(rendered string) bool {
	for _, line := range nonEmptyTail(rendered, busyScanLines) {
		lc := strings.ToLower(normalizeLineForMatch(line))
		for _, marker := range codexBusyMarkers {
			if strings.Contains(lc, marker) {
				return true
			}
		}
	}
	return false
}

// busyScanLines bounds how many non-empty tail lines CodexBusy inspects.
// Codex renders the active "Esc to interrupt" / "Thinking…" indicators
// near the input box (typically the last 2–4 lines: input row + hint +
// status). Matching PromptReady's tail size keeps the two signals
// symmetric. Anything larger risks pulling in tool-output lines from
// the just-completed turn that mention these strings in flavor text.
const busyScanLines = 10

// PromptReady detects Codex CLI v0.132.0's idle input prompt shape. The stable
// visual cue is a bottom input line beginning with `›` followed by a status line
// such as `gpt-5.5 xhigh · ~/repo`. Lines are normalized (NBSP → space) before
// matching so locale-driven non-breaking-space substitution doesn't break the
// regex.
func PromptReady(rendered string) bool {
	if !strings.Contains(rendered, "OpenAI Codex") {
		return false
	}
	for _, line := range nonEmptyTail(rendered, 10) {
		if codexInputPrompt.MatchString(normalizeLineForMatch(line)) {
			return true
		}
	}
	return false
}

// normalizeLineForMatch folds non-breaking spaces (U+00A0) and ideographic
// spaces to a regular ASCII space before regex/substring matching. Codex's TUI
// has historically emitted ` ` after `›` and `·`; gastown hit the same issue
// in their issue #1387 and we adopt the same fix.
func normalizeLineForMatch(line string) string {
	if !strings.ContainsAny(line, " 　") {
		return line
	}
	r := strings.NewReplacer(" ", " ", "　", " ")
	return r.Replace(line)
}

// StripTrailingPrompt removes the final Codex input prompt redraw from a full
// pane capture.
//
// Matches go through normalizeLineForMatch so a status line emitted with
// non-breaking spaces (locale-dependent) is still recognized. PromptReady
// has the same normalization; we need them in sync or one of the strip-
// vs-detect halves leaks the trailing prompt into ExtractCommandOutput.
func StripTrailingPrompt(rendered string) string {
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	for i := end - 1; i >= 0 && i >= end-12; i-- {
		if !codexStatusLine.MatchString(normalizeLineForMatch(lines[i])) {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-4; j-- {
			if codexInputPrompt.MatchString(normalizeLineForMatch(lines[j])) {
				return strings.TrimRight(strings.Join(lines[:j], "\n"), "\n")
			}
		}
	}
	return strings.TrimRight(rendered, "\n")
}

// ExtractCommandOutput strips the initial TUI chrome and final prompt redraw,
// returning the content after the echoed marker when it is present.
//
// For slash commands the marker is the prompt itself (we look for a line
// beginning with `› <marker>`); for free-form prompts the marker is the
// per-run sentinel suffix (we match any line containing the sentinel — it
// appears as an indented continuation of the echoed input block).
func ExtractCommandOutput(rendered, marker string, isSlashCommand bool) string {
	return ExtractCommandOutputWithStatus(rendered, marker, isSlashCommand).Output
}

func ExtractCommandOutputWithStatus(rendered, marker string, isSlashCommand bool) ExtractedOutput {
	if marker == "" {
		return ExtractedOutput{}
	}
	stripped := StripTrailingPrompt(rendered)
	lines := strings.Split(strings.ReplaceAll(stripped, "\r\n", "\n"), "\n")
	lastEcho := -1
	for i, line := range lines {
		normalized := normalizeLineForMatch(line)
		if isSlashCommand {
			if strings.HasPrefix(strings.TrimSpace(normalized), "› "+marker) {
				lastEcho = i
			}
		} else {
			if strings.Contains(normalized, marker) {
				lastEcho = i
			}
		}
	}
	if lastEcho >= 0 {
		out := strings.Join(lines[lastEcho+1:], "\n")
		return ExtractedOutput{Output: CleanCapture(strings.Trim(out, "\n")), CommandEchoFound: true}
	}
	// Slash-command fallback: codex renders some slash commands (e.g.
	// `/review the pending changes`) with a custom header like
	// `>> Code review started: ... <<` instead of echoing `› /cmd`.
	// In that case fall back to taking everything after the codex banner
	// box (the `╰…╯` line) as the output. Honest fallback — we still
	// mark CommandEchoFound=true because the run produced real content.
	if isSlashCommand {
		body := postBannerContent(lines)
		if strings.TrimSpace(body) != "" {
			return ExtractedOutput{Output: CleanCapture(body), CommandEchoFound: true}
		}
	}
	return ExtractedOutput{}
}

// postBannerContent returns everything after the closing line of codex's
// startup banner box (matched by the `╰…╯` glyphs). If no banner is found,
// returns the joined input — better to over-include than drop content.
func postBannerContent(lines []string) string {
	bannerEnd := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "╰") && strings.HasSuffix(trimmed, "╯") {
			bannerEnd = i
		}
	}
	if bannerEnd < 0 {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[bannerEnd+1:], "\n")
}

// CleanCapture drops lines that are empty, whitespace-only, or composed
// entirely of non-ASCII decorative characters (Unicode box-drawing,
// horizontal separators). Used both to clean the extracted output and the
// raw pane diagnostic so trailing dead space doesn't bury real content.
func CleanCapture(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if isEmptyOrDecorative(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isEmptyOrDecorative reports whether a line carries no content the user
// would want in the captured output. A line is decorative iff every
// non-whitespace rune is in the known-decorative set (box-drawing,
// block elements, em-dash/horizontal-bar separators). Any ASCII letter,
// CJK character, emoji, or other content rune keeps the line — the
// earlier "all non-ASCII = drop" heuristic incorrectly nuked localized
// prose and Unicode-only code/output.
func isEmptyOrDecorative(line string) bool {
	if strings.TrimSpace(line) == "" {
		return true
	}
	for _, r := range line {
		if r == ' ' || r == '\t' {
			continue
		}
		if isDecorativeRune(r) {
			continue
		}
		return false
	}
	return true
}

// isDecorativeRune is the explicit set of Unicode ranges/runes codex uses
// for visual chrome: box-drawing, block elements, and the two long-dash
// separators that show up in `─ Worked for Xm Ys ─` etc.
func isDecorativeRune(r rune) bool {
	switch {
	case r >= 0x2500 && r <= 0x257F: // Box Drawing
		return true
	case r >= 0x2580 && r <= 0x259F: // Block Elements
		return true
	case r == 0x2014, r == 0x2015: // em dash, horizontal bar
		return true
	}
	return false
}

var (
	codexInputPrompt = regexp.MustCompile(`^\s*›(?:\s+.*)?$`)
	codexStatusLine  = regexp.MustCompile(`^\s*\S+\s+(low|medium|high|xhigh)\s+·\s+.+$`)
)

func codexUpdatePrompt(rendered string) bool {
	return strings.Contains(rendered, "Update available!") &&
		strings.Contains(rendered, "Press enter to continue")
}

func codexUpdateChoices(rendered string) bool {
	return strings.Contains(rendered, "Update available!") &&
		strings.Contains(rendered, "Update now") &&
		strings.Contains(rendered, "Skip")
}

func codexPressEnterDialog(rendered string) bool {
	return strings.Contains(rendered, "Press enter to continue")
}

func codexTrustDialog(rendered string) bool {
	return strings.Contains(rendered, "Do you trust the contents of this directory?") ||
		strings.Contains(rendered, "trust this folder")
}

func tmuxCommandExited(rendered string) bool {
	return strings.Contains(rendered, "[ateam tmux command exited ")
}

func dismissStartupDialog(ctx context.Context, sess *tmuxctl.Session, rendered string) (bool, error) {
	switch {
	case codexUpdateChoices(rendered):
		if err := sess.SendLiteral(ctx, "2"); err != nil {
			return false, err
		}
		if err := sess.SendKeys(ctx, "Enter"); err != nil {
			return false, err
		}
		return true, nil
	case codexUpdatePrompt(rendered), codexPressEnterDialog(rendered), codexTrustDialog(rendered):
		if err := sess.SendKeys(ctx, "Enter"); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, nil
	}
}

func validateCommandOutput(initialInput string, extracted ExtractedOutput) error {
	if !extracted.CommandEchoFound {
		return fmt.Errorf("codex TUI did not accept initial input %q; startup dialog or prompt detection likely intercepted it", initialInput)
	}
	if strings.TrimSpace(extracted.Output) == "" {
		return fmt.Errorf("codex TUI produced no output for %q", initialInput)
	}
	if line, ok := codexErrorLine(extracted.Output); ok {
		return fmt.Errorf("codex TUI returned an error for %q: %s", initialInput, line)
	}
	return nil
}

func codexErrorLine(output string) (string, bool) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "■ {") && strings.Contains(line, `"type":"error"`) {
			return line, true
		}
		if strings.HasPrefix(line, `{"type":"error"`) {
			return line, true
		}
	}
	return "", false
}

func nonEmptyTail(rendered string, n int) []string {
	all := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	var lines []string
	for _, line := range all {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func stableHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sleepPoll(ctx context.Context) error {
	return sleepShort(ctx, pollInterval)
}

// sleepShort waits d, honoring ctx cancellation.
func sleepShort(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func effectiveBusyTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline) - time.Second
		if remaining > DefaultBusyTimeout {
			return remaining
		}
	}
	return DefaultBusyTimeout
}

// SessionName derives the deterministic tmux session name for a given EXEC_ID.
// Exported so the agent layer can compute the same name when it needs to
// reach into a live session (e.g. for postmortem capture in tests).
func SessionName(execID int64) string {
	return fmt.Sprintf("ateam-codex-%d", execID)
}

// SocketPath derives the tmux socket path for a given ateam project dir and
// EXEC_ID. Default lives under <ProjectDir>/cache/tmux/.
//
// IMPORTANT: `projectDir` here is ateam's resolved `.ateam/` directory (what
// ResolvedEnv.ProjectDir returns) — NOT the project root. Joining `.ateam`
// again would produce `.ateam/.ateam/cache/...` (the bug we hit in v1.1).
//
// Falls back to /tmp when the natural path would exceed
// sockaddr_un.sun_path (104 bytes on macOS, 108 on Linux). A deep checkout
// path plus the in-tree cache dir can blow past that limit; tmux then
// fails to bind with "File name too long".
func SocketPath(projectDir string, execID int64) string {
	abs := filepath.Join(projectDir, "cache", "tmux", fmt.Sprintf("exec-%d.sock", execID))
	if len(abs) <= sunPathSafeMax {
		return abs
	}
	// Hash the would-be path so concurrent runs from different projects
	// don't collide in /tmp. EXEC_ID alone isn't unique across projects.
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join("/tmp", fmt.Sprintf("ateam-codex-%x-%d.sock", sum[:4], execID))
}

// sunPathSafeMax is the largest absolute socket path we'll trust. macOS's
// sockaddr_un.sun_path is 104 bytes; Linux's is 108. 100 leaves a small
// margin so tmux's own decoration of the path (relative prefix, etc.)
// can't push us over the wire.
const sunPathSafeMax = 100

func envList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
