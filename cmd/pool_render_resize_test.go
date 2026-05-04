package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestResizeAwareWriterPassThrough verifies that without a pending
// resize, writes go straight through unchanged.
func TestResizeAwareWriterPassThrough(t *testing.T) {
	var buf bytes.Buffer
	w := newResizeAwareWriter(&buf, "HEADER")
	if _, err := w.Write([]byte("frame1")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); got != "frame1" {
		t.Errorf("got %q, want %q", got, "frame1")
	}
}

// TestResizeAwareWriterEmitsClearOnResize verifies that markResized
// causes the next Write to be preceded by a clear-screen + header.
func TestResizeAwareWriterEmitsClearOnResize(t *testing.T) {
	var buf bytes.Buffer
	w := newResizeAwareWriter(&buf, "HEADER")

	w.markResized()
	if _, err := w.Write([]byte("frame2")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got := buf.String()
	for _, want := range []string{"\x1b[H", "\x1b[2J", "\x1b[3J", "HEADER\n", "frame2"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n--- got ---\n%q", want, got)
		}
	}

	// Ordering check: the clear sequence must precede the header which
	// must precede the frame.
	clearIdx := strings.Index(got, "\x1b[3J")
	headerIdx := strings.Index(got, "HEADER\n")
	frameIdx := strings.Index(got, "frame2")
	if !(clearIdx < headerIdx && headerIdx < frameIdx) {
		t.Errorf("expected clear < header < frame, got indices %d < %d < %d", clearIdx, headerIdx, frameIdx)
	}
}

// TestResizeAwareWriterPendingResetsAfterFirstWrite verifies that the
// pending flag is one-shot — only the first Write after a resize gets
// the clear, subsequent writes are normal frames.
func TestResizeAwareWriterPendingResetsAfterFirstWrite(t *testing.T) {
	var buf bytes.Buffer
	w := newResizeAwareWriter(&buf, "HEADER")

	w.markResized()
	w.Write([]byte("a"))
	mid := buf.Len()
	w.Write([]byte("b"))

	tail := buf.String()[mid:]
	if strings.Contains(tail, "\x1b[3J") {
		t.Errorf("second write should not include clear escape; got %q", tail)
	}
	if strings.Contains(tail, "HEADER") {
		t.Errorf("second write should not re-emit header; got %q", tail)
	}
	if !strings.Contains(tail, "b") {
		t.Errorf("second write missing payload; got %q", tail)
	}
}

// TestResizeAwareWriterMultipleResizes verifies that two resizes
// before a Write result in only one clear (the flag is a boolean, not
// a counter — the second resize is absorbed by the first pending).
func TestResizeAwareWriterMultipleResizes(t *testing.T) {
	var buf bytes.Buffer
	w := newResizeAwareWriter(&buf, "HEADER")

	w.markResized()
	w.markResized()
	w.Write([]byte("frame"))

	got := buf.String()
	if n := strings.Count(got, "\x1b[3J"); n != 1 {
		t.Errorf("expected exactly 1 clear, got %d in %q", n, got)
	}
}
