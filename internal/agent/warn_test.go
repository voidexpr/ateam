package agent

import (
	"bytes"
	"strings"
	"testing"
)

func TestSetWarnWriterRedirectsAndRestores(t *testing.T) {
	var buf bytes.Buffer

	prev := SetWarnWriter(&buf)
	defer SetWarnWriter(prev)

	Warnf("Warning: skipping malformed claude JSONL line: %v\n",
		"json: cannot unmarshal array into Go struct field .content of type string")

	got := buf.String()
	if !strings.Contains(got, "skipping malformed claude JSONL line") {
		t.Fatalf("warning not captured by sink: %q", got)
	}
	if !strings.Contains(got, "cannot unmarshal") {
		t.Fatalf("error detail missing from sink output: %q", got)
	}
}

func TestSetWarnWriterNilRestoresStderr(t *testing.T) {
	var buf bytes.Buffer
	SetWarnWriter(&buf)
	prev := SetWarnWriter(nil)
	if prev != &buf {
		t.Errorf("expected previous writer to be the buffer, got %T", prev)
	}
	// After SetWarnWriter(nil), warnings should go to os.Stderr — not to
	// our buffer. Just verify the buffer doesn't grow.
	before := buf.Len()
	Warnf("should not land in buffer\n")
	if buf.Len() != before {
		t.Errorf("Warnf wrote to buffer after restore: %q", buf.String())
	}
}
