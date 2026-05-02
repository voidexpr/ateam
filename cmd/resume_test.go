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
