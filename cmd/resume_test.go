package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractSessionID(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "s.jsonl")
	body := `{"type":"system","subtype":"init","session_id":"abc-123","model":"claude-sonnet-4-6"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"system","subtype":"init","session_id":"later-id"}
`
	if err := os.WriteFile(stream, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := extractSessionID(stream)
	if err != nil {
		t.Fatalf("extractSessionID: %v", err)
	}
	if got != "abc-123" {
		t.Errorf("session id = %q, want abc-123", got)
	}
}

func TestExtractSessionIDEmpty(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "s.jsonl")
	body := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}` + "\n"
	if err := os.WriteFile(stream, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := extractSessionID(stream)
	if err != nil {
		t.Fatalf("extractSessionID: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty session id, got %q", got)
	}
}

func TestExtractSessionIDMissingFile(t *testing.T) {
	if _, err := extractSessionID("/no/such/file"); err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestExtractSessionIDCodexThreadID(t *testing.T) {
	dir := t.TempDir()
	stream := filepath.Join(dir, "s.jsonl")
	body := `{"type":"thread.started","thread_id":"019df527-3195-79d1-a838-9adc1bebae81"}
{"type":"turn.started"}
`
	if err := os.WriteFile(stream, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := extractSessionID(stream)
	if err != nil {
		t.Fatalf("extractSessionID: %v", err)
	}
	if got != "019df527-3195-79d1-a838-9adc1bebae81" {
		t.Errorf("session id = %q, want codex thread id", got)
	}
}

func TestResumeCommand(t *testing.T) {
	// Make sure env-var overrides from the host don't leak into the
	// default-binary assertions below.
	t.Setenv(envResumeClaudeCmd, "")
	t.Setenv(envResumeCodexCmd, "")

	tests := []struct {
		agent   string
		wantBin string
		wantArg string // first arg
	}{
		{"claude", "claude", "--resume"},
		{"codex", "codex", "resume"},
		{"codex-tmux", "codex", "resume"},
	}
	for _, tt := range tests {
		bin, args := resumeCommand(tt.agent, "abc")
		if bin != tt.wantBin {
			t.Errorf("agent=%s: bin = %q, want %q", tt.agent, bin, tt.wantBin)
		}
		if len(args) == 0 || args[0] != tt.wantArg {
			t.Errorf("agent=%s: first arg = %v, want %q", tt.agent, args, tt.wantArg)
		}
		if args[len(args)-1] != "abc" {
			t.Errorf("agent=%s: last arg = %q, want session id", tt.agent, args[len(args)-1])
		}
	}
	// codex (and codex-tmux) require --include-non-interactive (else
	// `exec --json` sessions are hidden from the picker).
	for _, a := range []string{"codex", "codex-tmux"} {
		_, args := resumeCommand(a, "abc")
		found := false
		for _, x := range args {
			if x == "--include-non-interactive" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s resume args missing --include-non-interactive: %v", a, args)
		}
	}
}

func TestResumeCommandEnvOverride(t *testing.T) {
	t.Setenv(envResumeClaudeCmd, "my-claude --foo")
	t.Setenv(envResumeCodexCmd, "/opt/bin/codex-wrap")

	bin, args := resumeCommand("claude", "sess-1")
	wantArgs := []string{"--foo", "--resume", "sess-1"}
	if bin != "my-claude" || !equalStrings(args, wantArgs) {
		t.Errorf("claude override: bin=%q args=%v, want my-claude %v", bin, args, wantArgs)
	}

	bin, args = resumeCommand("codex-tmux", "sess-2")
	wantArgs = []string{"resume", "--include-non-interactive", "sess-2"}
	if bin != "/opt/bin/codex-wrap" || !equalStrings(args, wantArgs) {
		t.Errorf("codex-tmux override: bin=%q args=%v, want /opt/bin/codex-wrap %v", bin, args, wantArgs)
	}
}

func TestResumeBinaryFallback(t *testing.T) {
	t.Setenv(envResumeClaudeCmd, "   ")
	bin, prefix := resumeBinary(envResumeClaudeCmd, "claude")
	if bin != "claude" || len(prefix) != 0 {
		t.Errorf("whitespace env should fall back: bin=%q prefix=%v", bin, prefix)
	}
}

func TestResolveSessionIDCodexTmuxFromResultEvent(t *testing.T) {
	// Codex-tmux runs where the live tailer never wrote a thread.started
	// line: the synthetic result event carries the session id (new field)
	// or, failing that, the rollout filename embeds the UUID.
	dir := t.TempDir()
	stream := filepath.Join(dir, "agent.jsonl")

	const sid = "019e51e6-fcb4-7053-a700-0bdf7662e1a5"
	// Old-format result: no explicit session_id, must fall back to filename.
	oldLine := `{"type":"result","tmux_session_name":"ateam-codex-141",` +
		`"session_log":"/Users/x/.codex/sessions/2026/05/22/rollout-2026-05-22T15-55-53-` + sid + `.jsonl"}`
	body := `{"type":"tmux.start","tmux_session_name":"ateam-codex-141"}
` + oldLine + "\n"
	if err := os.WriteFile(stream, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := resolveSessionID(stream, "codex-tmux")
	if err != nil {
		t.Fatalf("resolveSessionID: %v", err)
	}
	if got != sid {
		t.Errorf("session id = %q, want %q (from rollout filename)", got, sid)
	}

	// Generic extractor (no agent hint) ignores the synthetic result event.
	got, _ = extractSessionID(stream)
	if got != "" {
		t.Errorf("extractSessionID alone should not find codex-tmux result id, got %q", got)
	}

	// New-format result with explicit session_id wins over the filename.
	const newSID = "abcdef01-2345-6789-abcd-ef0123456789"
	newLine := `{"type":"result","tmux_session_name":"x","session_id":"` + newSID + `",` +
		`"session_log":"/tmp/rollout-2026-05-22T00-00-00-` + sid + `.jsonl"}`
	if err := os.WriteFile(stream, []byte(newLine+"\n"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = resolveSessionID(stream, "codex-tmux")
	if err != nil {
		t.Fatalf("resolveSessionID: %v", err)
	}
	if got != newSID {
		t.Errorf("session id = %q, want explicit %q", got, newSID)
	}

	// Non-codex-tmux agents do not get the fallback.
	got, _ = resolveSessionID(stream, "codex")
	if got != "" {
		t.Errorf("non-tmux agent should not trigger fallback, got %q", got)
	}
}

func TestResumeNativeCommandLine(t *testing.T) {
	t.Setenv(envResumeClaudeCmd, "")
	t.Setenv(envResumeCodexCmd, "")

	cases := map[string]string{
		"claude":     "claude --resume abc",
		"codex":      "codex resume --include-non-interactive abc",
		"codex-tmux": "codex resume --include-non-interactive abc",
	}
	for agent, want := range cases {
		if got := resumeNativeCommandLine(agent, "abc"); got != want {
			t.Errorf("agent=%s: %q, want %q", agent, got, want)
		}
	}

	// Env override flows through to the shared formatter (this is what
	// `ateam inspect` uses when surfacing the resume hint).
	t.Setenv(envResumeCodexCmd, "/opt/codex --foo")
	want := "/opt/codex --foo resume --include-non-interactive abc"
	if got := resumeNativeCommandLine("codex-tmux", "abc"); got != want {
		t.Errorf("codex env override: %q, want %q", got, want)
	}
}

func TestIsResumableAgent(t *testing.T) {
	for _, a := range []string{"claude", "codex", "codex-tmux"} {
		if !isResumableAgent(a) {
			t.Errorf("%s should be resumable", a)
		}
	}
	for _, a := range []string{"mock", "", "claude-something"} {
		if isResumableAgent(a) {
			t.Errorf("%s should NOT be resumable", a)
		}
	}
}

func TestReadSpecifiedEnv(t *testing.T) {
	dir := t.TempDir()
	execMD := filepath.Join(dir, "x_exec.md")
	body := `# Command
* started: x

# Env
## Inherited
PATH=/bin
HOME=/root

## Specified
unsets CLAUDECODE
CLAUDE_CONFIG_DIR=/custom/cfg
OTHER=value

# Settings
` + "```json\n{}\n```" + `
`
	if err := os.WriteFile(execMD, []byte(body), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if v, ok := readSpecifiedEnv(execMD, "CLAUDE_CONFIG_DIR"); !ok || v != "/custom/cfg" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q ok=%v, want /custom/cfg true", v, ok)
	}
	if v, ok := readSpecifiedEnv(execMD, "OTHER"); !ok || v != "value" {
		t.Errorf("OTHER = %q ok=%v, want value true", v, ok)
	}
	if v, ok := readSpecifiedEnv(execMD, "PATH"); ok {
		// PATH is in the Inherited section; readSpecifiedEnv must not look there.
		t.Errorf("PATH should not be returned from Specified, got %q", v)
	}
	if _, ok := readSpecifiedEnv(execMD, "MISSING"); ok {
		t.Errorf("MISSING should not be found")
	}
}
