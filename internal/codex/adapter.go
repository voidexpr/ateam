// Package codex contains Codex CLI adapter details that are specific to the
// interactive TUI.
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
)

var unattendedDisabledFeatures = []string{"apps", "plugins"}

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
}

type TmuxResult struct {
	Output      string
	RawCapture  string
	SessionName string
	IdleSignal  IdleSignal
	Duration    time.Duration
}

type ExtractedOutput struct {
	Output           string
	CommandEchoFound bool
}

// InteractiveArgs builds the argv for interactive Codex TUI mode.
func InteractiveArgs(base []string, model, effort string, extra []string) []string {
	args := make([]string, len(base))
	copy(args, base)
	args = withInteractiveDefaults(args, extra)
	if model != "" {
		args = append(args, "--model", model)
	}
	if effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+effort)
	}
	return append(args, extra...)
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

	sessionName := sessionName(started)
	args := InteractiveArgs(cfg.Args, cfg.Model, cfg.Effort, cfg.ExtraArgs)
	cmd := append([]string{cfg.Command}, args...)

	sess, err := tmuxctl.New(ctx, sessionName, cfg.Width, cfg.Height, envList(cfg.Env), cfg.WorkDir, cmd, cfg.CmdFactory)
	if err != nil {
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}
	defer func() {
		_ = sess.Kill(context.Background())
	}()

	ready, err := waitReady(ctx, sess, cfg.StartTimeout)
	if err != nil {
		raw, _ := sess.Capture(context.Background(), true)
		return TmuxResult{RawCapture: raw, SessionName: sessionName, Duration: time.Since(started)}, err
	}

	if err := sess.TypeLiteral(ctx, cfg.InitialInput); err != nil {
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}
	if err := waitInputEcho(ctx, sess, cfg.InitialInput, 2*time.Second); err != nil {
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}
	if err := sess.SendKeys(ctx, "C-Enter"); err != nil {
		return TmuxResult{SessionName: sessionName, Duration: time.Since(started)}, err
	}

	idleSignal, err := waitIdle(ctx, sess, ready, cfg.BusyTimeout, cfg.QuiescenceWindow)
	raw, capErr := sess.Capture(context.Background(), true)
	if capErr != nil && err == nil {
		err = capErr
	}
	extracted := ExtractCommandOutputWithStatus(raw, cfg.InitialInput)
	out := extracted.Output
	result := TmuxResult{
		Output:      out,
		RawCapture:  raw,
		SessionName: sessionName,
		IdleSignal:  idleSignal,
		Duration:    time.Since(started),
	}
	if err != nil {
		return result, err
	}
	if err := validateCommandOutput(cfg.InitialInput, extracted); err != nil {
		return result, err
	}
	return result, nil
}

func waitReady(ctx context.Context, sess *tmuxctl.Session, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rendered, err := sess.Capture(ctx, true)
		if err != nil {
			return "", err
		}
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

func waitInputEcho(ctx context.Context, sess *tmuxctl.Session, input string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rendered, err := sess.Capture(ctx, true)
		if err != nil {
			return err
		}
		if strings.Contains(rendered, input) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("codex TUI did not echo initial input %q within %s", input, timeout)
		}
		if err := sleepPoll(ctx); err != nil {
			return err
		}
	}
}

func waitIdle(ctx context.Context, sess *tmuxctl.Session, initial string, timeout, quiescence time.Duration) (IdleSignal, error) {
	deadline := time.Now().Add(timeout)
	initialHash := stableHash(initial)
	lastHash := initialHash
	lastChanged := time.Now()
	promptWasReady := true

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
		}

		ready := PromptReady(rendered)
		if ready && !promptWasReady {
			return IdleSignalPromptMatch, nil
		}
		if !ready {
			promptWasReady = false
		}
		if hash != initialHash && time.Since(lastChanged) >= quiescence {
			return IdleSignalQuiescence, nil
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

// PromptReady detects Codex CLI v0.132.0's idle input prompt shape. The stable
// visual cue is a bottom input line beginning with `›` followed by a status line
// such as `gpt-5.5 xhigh · ~/repo`.
func PromptReady(rendered string) bool {
	if !strings.Contains(rendered, "OpenAI Codex") {
		return false
	}
	lines := nonEmptyTail(rendered, 10)
	for _, line := range lines {
		if !codexInputPrompt.MatchString(line) {
			continue
		}
		return true
	}
	return false
}

// StripTrailingPrompt removes the final Codex input prompt redraw from a full
// pane capture.
func StripTrailingPrompt(rendered string) string {
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	for i := end - 1; i >= 0 && i >= end-12; i-- {
		if !codexStatusLine.MatchString(lines[i]) {
			continue
		}
		for j := i - 1; j >= 0 && j >= i-4; j-- {
			if codexInputPrompt.MatchString(lines[j]) {
				return strings.TrimRight(strings.Join(lines[:j], "\n"), "\n")
			}
		}
	}
	return strings.TrimRight(rendered, "\n")
}

// ExtractCommandOutput strips the initial TUI chrome and final prompt redraw,
// returning the content after the echoed initial input when it is present.
func ExtractCommandOutput(rendered, initialInput string) string {
	return ExtractCommandOutputWithStatus(rendered, initialInput).Output
}

func ExtractCommandOutputWithStatus(rendered, initialInput string) ExtractedOutput {
	stripped := StripTrailingPrompt(rendered)
	lines := strings.Split(strings.ReplaceAll(stripped, "\r\n", "\n"), "\n")
	lastCommand := -1
	for i, line := range lines {
		if initialInput != "" && strings.HasPrefix(strings.TrimSpace(line), "› "+initialInput) {
			lastCommand = i
		}
	}
	if lastCommand < 0 {
		return ExtractedOutput{}
	}
	out := strings.Join(lines[lastCommand+1:], "\n")
	return ExtractedOutput{Output: strings.Trim(out, "\n"), CommandEchoFound: true}
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

func withInteractiveDefaults(args, extra []string) []string {
	detectArgs := append(append([]string{}, args...), extra...)
	if !hasOption(detectArgs, "--no-alt-screen") {
		args = append(args, "--no-alt-screen")
	}
	if !hasOption(detectArgs, "--sandbox", "-s") && !hasOption(detectArgs, "--dangerously-bypass-approvals-and-sandbox") {
		args = append(args, "-s", "workspace-write")
	}
	if !hasOption(detectArgs, "--ask-for-approval", "-a") && !hasOption(detectArgs, "--dangerously-bypass-approvals-and-sandbox") {
		args = append(args, "-a", "never")
	}
	if !hasConfigOverride(detectArgs, updateCheckConfigKey) {
		args = append(args, "-c", updateCheckConfigKey+"=false")
	}
	for _, feature := range unattendedDisabledFeatures {
		if !hasFeatureOverride(detectArgs, feature) && !hasConfigOverride(detectArgs, "features."+feature) {
			args = append(args, "--disable", feature)
		}
	}
	return args
}

func hasOption(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name || strings.HasPrefix(arg, name+"=") {
				return true
			}
		}
	}
	return false
}

func hasConfigOverride(args []string, key string) bool {
	for i, arg := range args {
		switch {
		case arg == "-c" || arg == "--config":
			if i+1 < len(args) && configOverrideKey(args[i+1]) == key {
				return true
			}
		case strings.HasPrefix(arg, "--config="):
			if configOverrideKey(strings.TrimPrefix(arg, "--config=")) == key {
				return true
			}
		}
	}
	return false
}

func hasFeatureOverride(args []string, feature string) bool {
	for i, arg := range args {
		switch {
		case arg == "--enable" || arg == "--disable":
			if i+1 < len(args) && args[i+1] == feature {
				return true
			}
		case strings.HasPrefix(arg, "--enable="):
			if strings.TrimPrefix(arg, "--enable=") == feature {
				return true
			}
		case strings.HasPrefix(arg, "--disable="):
			if strings.TrimPrefix(arg, "--disable=") == feature {
				return true
			}
		}
	}
	return false
}

func configOverrideKey(value string) string {
	value = strings.TrimSpace(value)
	key, _, _ := strings.Cut(value, "=")
	return strings.TrimSpace(key)
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
	t := time.NewTimer(pollInterval)
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

func sessionName(t time.Time) string {
	return fmt.Sprintf("ateam-codex-%d", t.UnixNano())
}

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
