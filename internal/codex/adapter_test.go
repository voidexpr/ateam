package codex

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInteractiveArgs(t *testing.T) {
	got := InteractiveArgs(
		[]string{"--no-alt-screen", "--sandbox", "workspace-write"},
		"gpt-5.5",
		"xhigh",
		[]string{"--ask-for-approval", "never"},
	)
	want := []string{
		"--no-alt-screen",
		"--sandbox", "workspace-write",
		"-c", "check_for_update_on_startup=false",
		"--disable", "apps",
		"--disable", "plugins",
		"--model", "gpt-5.5",
		"-c", "model_reasoning_effort=xhigh",
		"--ask-for-approval", "never",
	}
	if !slices.Equal(got, want) {
		t.Errorf("InteractiveArgs = %v, want %v", got, want)
	}
}

func TestInteractiveArgsAddsUnattendedDefaults(t *testing.T) {
	got := InteractiveArgs(nil, "", "", nil)
	want := []string{
		"--no-alt-screen",
		"-s", "workspace-write",
		"-a", "never",
		"-c", "check_for_update_on_startup=false",
		"--disable", "apps",
		"--disable", "plugins",
	}
	if !slices.Equal(got, want) {
		t.Errorf("InteractiveArgs = %v, want %v", got, want)
	}
}

func TestInteractiveArgsHonorsExplicitUnattendedOverrides(t *testing.T) {
	got := InteractiveArgs([]string{"-c", "check_for_update_on_startup=true"}, "", "", []string{"--enable", "apps", "-c", "features.plugins=true"})
	want := []string{
		"-c", "check_for_update_on_startup=true",
		"--no-alt-screen",
		"-s", "workspace-write",
		"-a", "never",
		"--enable", "apps",
		"-c", "features.plugins=true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("InteractiveArgs = %v, want %v", got, want)
	}
}

func TestPromptReadyCodex0132(t *testing.T) {
	rendered := `
╭──────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.132.0)                           │
│                                                      │
│ model:     gpt-5.5 xhigh   /model to change          │
│ directory: ~/SyncDatabox/nicmac/projects/ateam-codex │
╰──────────────────────────────────────────────────────╯

  Tip: New Use /fast to enable our fastest inference with increased plan usage.


› Improve documentation in @filename

  gpt-5.5 xhigh · ~/SyncDatabox/nicmac/projects/ateam-codex


`
	if !PromptReady(rendered) {
		t.Fatal("PromptReady = false, want true")
	}
}

func TestPromptReadyCodexSuggestion(t *testing.T) {
	rendered := `
╭──────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.132.0)                           │
│                                                      │
│ model:     gpt-5.5 xhigh   /model to change          │
│ directory: ~/SyncDatabox/nicmac/projects/ateam-codex │
╰──────────────────────────────────────────────────────╯

› Find and fix a bug in @filename

  gpt-5.5 xhigh · ~/SyncDatabox/nicmac/projects/ateam-codex
`
	if !PromptReady(rendered) {
		t.Fatal("PromptReady = false, want true")
	}
}

func TestStripTrailingPrompt(t *testing.T) {
	rendered := `Review result

Finding A

› Improve documentation in @filename

  gpt-5.5 xhigh · ~/repo

`
	got := StripTrailingPrompt(rendered)
	want := "Review result\n\nFinding A"
	if got != want {
		t.Errorf("StripTrailingPrompt = %q, want %q", got, want)
	}
}

func TestExtractCommandOutput(t *testing.T) {
	rendered := `╭────────────────────────╮
│ >_ OpenAI Codex        │
╰────────────────────────╯

› /review

Review result

Finding A

› Improve documentation in @filename

  gpt-5.5 xhigh · ~/repo

`
	got := ExtractCommandOutput(rendered, "/review", true)
	// CleanCapture strips blank lines; the empty line between the two
	// content paragraphs is intentionally dropped.
	want := "Review result\nFinding A"
	if got != want {
		t.Errorf("ExtractCommandOutput = %q, want %q", got, want)
	}
}

func TestExtractCommandOutputMultiLineSentinel(t *testing.T) {
	sentinel := "[ateam-end-deadbeef]"
	rendered := `╭────────────────────────╮
│ >_ OpenAI Codex        │
╰────────────────────────╯

› review the codebase
  end the response with a summary
  ` + sentinel + `

Here is the review.

Item A
Item B

› Improve documentation in @filename

  gpt-5.5 xhigh · ~/repo
`
	got := ExtractCommandOutputWithStatus(rendered, sentinel, false)
	if !got.CommandEchoFound {
		t.Fatalf("CommandEchoFound = false, want true (capture:\n%s)", rendered)
	}
	want := "Here is the review.\nItem A\nItem B"
	if got.Output != want {
		t.Errorf("Output = %q, want %q", got.Output, want)
	}
}

// TestExtractSlashCommandFallback: when codex renders a slash command with a
// custom header instead of the standard `› /cmd` echo (e.g. /review with an
// inline argument), extraction falls back to post-banner content.
func TestExtractSlashCommandFallback(t *testing.T) {
	rendered := `╭──────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.132.0)                           │
│                                                      │
│ model:     gpt-5.4-mini low   /model to change       │
│ directory: ~/repo                                    │
╰──────────────────────────────────────────────────────╯

  Tip: Paste an image with Ctrl+V to attach it to your next message.

>> Code review started: the pending changes <<

• Ran git diff --stat
  └ summary of changes

<< Code review finished >>

────────────────────────────────────────

• Finding: socket path can exceed sun_path limit on deep checkouts.

› Improve documentation in @filename

  gpt-5.4-mini low · ~/repo
`
	got := ExtractCommandOutputWithStatus(rendered, "/review the pending changes", true)
	if !got.CommandEchoFound {
		t.Fatalf("CommandEchoFound = false; fallback should accept post-banner content")
	}
	if !strings.Contains(got.Output, "Code review started") {
		t.Errorf("Output missing 'Code review started':\n%s", got.Output)
	}
	if !strings.Contains(got.Output, "Finding: socket path") {
		t.Errorf("Output missing review finding:\n%s", got.Output)
	}
	if strings.Contains(got.Output, "OpenAI Codex") {
		t.Errorf("Output leaked banner chrome:\n%s", got.Output)
	}
	if strings.Contains(got.Output, "Improve documentation") {
		t.Errorf("Output leaked trailing prompt suggestion:\n%s", got.Output)
	}
}

func TestCleanCapture(t *testing.T) {
	in := "First line\n\n  \n╭──────╮\n│ Title │\n╰──────╯\n────────\nSecond line\n\n\n"
	got := CleanCapture(in)
	// Pure-decorative lines (╭──╮ ╰──╯ ────) and blanks dropped;
	// `│ Title │` survives because it contains ASCII chars.
	want := "First line\n│ Title │\nSecond line"
	if got != want {
		t.Errorf("CleanCapture =\n%q\nwant\n%q", got, want)
	}
}

func TestSocketPathFallsBackOnLongPath(t *testing.T) {
	// Short path: stays in-tree.
	short := SocketPath("/p/.ateam", 1)
	if !strings.HasPrefix(short, "/p/.ateam/cache/tmux/") {
		t.Errorf("short path = %q, expected in-tree under projectDir", short)
	}
	// Long path: spills over sunPathSafeMax → falls back to /tmp.
	long := SocketPath(strings.Repeat("/long-segment", 10)+"/.ateam", 1)
	if !strings.HasPrefix(long, "/tmp/ateam-codex-") {
		t.Errorf("long path = %q, expected /tmp fallback", long)
	}
	if len(long) > 100 {
		t.Errorf("fallback path %q still exceeds 100 bytes", long)
	}
}

func TestExtractCommandOutputRequiresEcho(t *testing.T) {
	rendered := `  ✨ Update available! 0.132.0 -> 0.133.0

  Press enter to continue

╭──────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.132.0)                           │
╰──────────────────────────────────────────────────────╯
`
	got := ExtractCommandOutputWithStatus(rendered, "ping", false)
	if got.CommandEchoFound {
		t.Fatal("CommandEchoFound = true, want false")
	}
	if got.Output != "" {
		t.Errorf("Output = %q, want empty", got.Output)
	}
	if err := validateCommandOutput("ping", got); err == nil {
		t.Fatal("validateCommandOutput returned nil, want error")
	}
}

func TestPreparePromptSlashCommand(t *testing.T) {
	d := preparePrompt("/review", 0)
	if !d.IsSlashCommand {
		t.Errorf("IsSlashCommand = false, want true for /review")
	}
	if d.SentText != "/review" {
		t.Errorf("SentText = %q, want %q (slash command must not be wrapped)", d.SentText, "/review")
	}
	if d.EchoMarker != "/review" {
		t.Errorf("EchoMarker = %q, want %q", d.EchoMarker, "/review")
	}
}

func TestPreparePromptFreeFormGetsSentinel(t *testing.T) {
	d := preparePrompt("Please review the changes", 0)
	if d.IsSlashCommand {
		t.Errorf("IsSlashCommand = true, want false")
	}
	if !strings.HasPrefix(d.EchoMarker, "[ateam-end-") || !strings.HasSuffix(d.EchoMarker, "]") {
		t.Errorf("EchoMarker = %q, want a sentinel prefixed with [ateam-end-", d.EchoMarker)
	}
	if !strings.HasSuffix(d.SentText, "\n"+d.EchoMarker) {
		t.Errorf("SentText should end with the sentinel marker on its own line, got %q", d.SentText)
	}
}

func TestPreparePromptMultiLineFreeForm(t *testing.T) {
	d := preparePrompt("Line one\nLine two\nLine three", 0)
	if d.IsSlashCommand {
		t.Errorf("IsSlashCommand = true, want false for multi-line")
	}
	// Multi-line slash-shaped first line is *not* a slash command.
	d2 := preparePrompt("/review\nplease look at @file.md", 0)
	if d2.IsSlashCommand {
		t.Errorf("IsSlashCommand = true, want false for multi-line `/`-prefixed prompt")
	}
}

func TestHeartbeatPreview(t *testing.T) {
	cases := []struct {
		name   string
		render string
		want   string
	}{
		{"empty", "", "(pane updated)"},
		{"single line", "Running tool: rg foo", "Running tool: rg foo"},
		{"trims trailing", "  hello   \n\n", "hello"},
		{
			"truncates at 80",
			strings.Repeat("a", 100),
			strings.Repeat("a", 80) + "…",
		},
	}
	for _, tc := range cases {
		got := heartbeatPreview(tc.render)
		if got != tc.want {
			t.Errorf("%s: preview = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCodexBusyDetector(t *testing.T) {
	cases := []struct {
		name   string
		render string
		want   bool
	}{
		{"esc to interrupt", "› doing things\n  Esc to interrupt", true},
		{"thinking ellipsis", "Thinking…", true},
		{"idle prompt", "› \n  gpt-5.5 xhigh · ~/repo", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		got := CodexBusy(tc.render)
		if got != tc.want {
			t.Errorf("%s: CodexBusy = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPromptReadyNormalizesNBSP(t *testing.T) {
	// `›` followed by U+00A0 (NBSP) instead of space — locale-driven shape
	// that broke literal-space regexes in gastown #1387.
	rendered := "╭───╮\n│ OpenAI Codex │\n╰───╯\n\n› \n  gpt-5.5 xhigh · ~/repo\n"
	if !PromptReady(rendered) {
		t.Fatalf("PromptReady = false; NBSP should be normalized")
	}
}

func TestRunTmuxFakeCodexTUI(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	dir := t.TempDir()
	// Socket under /tmp, fake script under t.TempDir(). sockaddr_un.sun_path
	// is 104 bytes on macOS; /var/folders/.../T/.../ paths from t.TempDir()
	// can spill over and trigger "File name too long" at bind time.
	socketDir, err := os.MkdirTemp("/tmp", "codexs-")
	if err != nil {
		t.Skipf("cannot create /tmp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "t.sock")

	fakeCodex := filepath.Join(dir, "fake-codex")
	script := `#!/bin/sh
draw_prompt() {
  printf '› '
  printf '\033[s\n\n  gpt-5.5 low · ~/repo\n\033[u'
}

printf '╭────────────────────╮\n'
printf '│ >_ OpenAI Codex    │\n'
printf '╰────────────────────╯\n\n'
draw_prompt

# Read multiple lines until the ateam end sentinel arrives — the agent
# wraps non-slash-command prompts with this marker so we know when to stop.
prompt=""
while IFS= read -r line; do
  case "$line" in
    *ateam-end-*) break ;;
  esac
  prompt="${prompt}${line} "
done

printf '\n• fake response for %s\n\n' "$prompt"
draw_prompt
sleep 86400
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0700); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := RunTmux(ctx, TmuxConfig{
		Command:          fakeCodex,
		Model:            "gpt-5.5",
		Effort:           "low",
		InitialInput:     "ping",
		WorkDir:          dir,
		StartTimeout:     3 * time.Second,
		BusyTimeout:      5 * time.Second,
		QuiescenceWindow: 100 * time.Millisecond,
		Width:            100,
		Height:           30,
		// Avoid `.` in the session name — tmux's target syntax treats it as a
		// session/window separator. Real production code uses an integer
		// EXEC_ID so this can't happen there.
		SessionName: "ateam-codex-test-" + time.Now().Format("150405000"),
		SocketPath:  socketPath,
	})
	if err != nil {
		if strings.Contains(err.Error(), "No such file or directory") ||
			strings.Contains(err.Error(), "Operation not permitted") {
			t.Skipf("tmux unavailable in this environment: %v", err)
		}
		t.Fatalf("RunTmux: %v\ncapture:\n%s", err, result.RawCapture)
	}
	if !strings.Contains(result.Output, "fake response for ping") {
		t.Fatalf("output missing fake response:\n%s\ncapture:\n%s", result.Output, result.RawCapture)
	}
	if strings.Contains(result.Output, "OpenAI Codex") {
		t.Fatalf("output leaked startup chrome:\n%s", result.Output)
	}
	if result.IdleSignal == IdleSignalTimeout {
		t.Fatalf("idle signal = timeout, want completion signal")
	}
}

func TestValidateCommandOutputRejectsEmptyResponse(t *testing.T) {
	err := validateCommandOutput("/review", ExtractedOutput{CommandEchoFound: true})
	if err == nil {
		t.Fatal("validateCommandOutput returned nil, want error")
	}
}

func TestValidateCommandOutputRejectsCodexError(t *testing.T) {
	err := validateCommandOutput("ping", ExtractedOutput{
		CommandEchoFound: true,
		Output: `⚠ Model metadata for gpt-5-mini not found.

■ {"type":"error","status":400,"error":{"type":"invalid_request_error","message":"unsupported model"}}`,
	})
	if err == nil {
		t.Fatal("validateCommandOutput returned nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported model") {
		t.Fatalf("error = %v, want unsupported model detail", err)
	}
}

func TestCodexStartupDialogDetectors(t *testing.T) {
	updateChoices := `✨ Update available!
› 1. Update now
  2. Skip`
	if !codexUpdateChoices(updateChoices) {
		t.Fatal("codexUpdateChoices = false, want true")
	}
	if !codexPressEnterDialog("Press enter to continue") {
		t.Fatal("codexPressEnterDialog = false, want true")
	}
	if !codexTrustDialog("Do you trust the contents of this directory?") {
		t.Fatal("codexTrustDialog = false, want true")
	}
	if !tmuxCommandExited("[ateam tmux command exited 1]") {
		t.Fatal("tmuxCommandExited = false, want true")
	}
}
