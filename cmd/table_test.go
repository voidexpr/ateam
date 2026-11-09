package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr swaps os.Stderr for a pipe while fn runs and returns whatever
// fn wrote. Mirrors captureStdout from exec_test.go for the cases where the
// code under test only writes to stderr (warnings, etc.).
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = pw
	fn()
	pw.Close()
	os.Stderr = old
	var buf bytes.Buffer
	io.Copy(&buf, pr)
	return buf.String()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestPrintArtifact verifies --print reads the on-disk artifact rather than
// the agent's stream output, matching the shipped single-file prompts'
// "harness reads it directly, anything you stream as text is discarded"
// contract. Stream fallback fires when the file is missing/empty (the
// prompts' documented "Write failed → emit body" recovery path).
func TestPrintArtifact(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "review.md")
	if err := os.WriteFile(filePath, []byte("# Real Review\n\nP0: ship it\n"), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.md")

	cases := []struct {
		name    string
		path    string
		stream  string
		want    string
		wantNot string
	}{
		{
			name:    "file exists — prefers file over stream",
			path:    filePath,
			stream:  "Review written to runtime/...",
			want:    "P0: ship it",
			wantNot: "Review written to",
		},
		{
			name:   "file missing — falls back to stream",
			path:   missing,
			stream: "stream body recovery",
			want:   "stream body recovery",
		},
		{
			name: "both empty — quiet no-op",
			path: missing,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				printArtifact(tc.path, tc.stream)
			})
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Errorf("missing %q in output:\n%s", tc.want, out)
			}
			if tc.wantNot != "" && strings.Contains(out, tc.wantNot) {
				t.Errorf("unexpected %q in output:\n%s", tc.wantNot, out)
			}
			if tc.want == "" && tc.stream == "" && out != "" {
				t.Errorf("expected no output, got %q", out)
			}
		})
	}
}
