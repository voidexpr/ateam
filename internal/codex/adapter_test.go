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
	got := ExtractCommandOutput(rendered, "/review")
	want := "Review result\n\nFinding A"
	if got != want {
		t.Errorf("ExtractCommandOutput = %q, want %q", got, want)
	}
}

func TestExtractCommandOutputRequiresEcho(t *testing.T) {
	rendered := `  ✨ Update available! 0.132.0 -> 0.133.0

  Press enter to continue

╭──────────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.132.0)                           │
╰──────────────────────────────────────────────────────╯
`
	got := ExtractCommandOutputWithStatus(rendered, "ping")
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

func TestRunTmuxFakeCodexTUI(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	dir := t.TempDir()
	socketDir := filepath.Join("/tmp", "ateam-codex-test-"+time.Now().Format("20060102150405.000000000"))
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	t.Setenv("ATEAM_TMUX_SOCKET_DIR", socketDir)
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })

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

IFS= read -r prompt
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
	})
	if err != nil {
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
