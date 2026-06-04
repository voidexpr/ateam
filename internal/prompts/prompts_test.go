package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveValueLiteral(t *testing.T) {
	got, err := ResolveValue("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestResolveValueFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.md")
	if err := os.WriteFile(path, []byte("from file"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveValue("@" + path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from file" {
		t.Errorf("got %q, want %q", got, "from file")
	}
}

func TestResolveValueStdin(t *testing.T) {
	for _, sentinel := range []string{"-", "@-"} {
		t.Run(sentinel, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			origStdin := os.Stdin
			os.Stdin = r
			t.Cleanup(func() { os.Stdin = origStdin })

			go func() {
				_, _ = w.Write([]byte("from stdin"))
				_ = w.Close()
			}()

			got, err := ResolveValue(sentinel)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != "from stdin" {
				t.Errorf("got %q, want %q", got, "from stdin")
			}
		})
	}
}
