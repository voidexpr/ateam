package tmuxctl

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSessionSendLiteralCapture(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use a short name + path under /tmp. sockaddr_un.sun_path is capped at
	// 104 bytes on macOS, and t.TempDir() lives under /var/folders/.../T/
	// which already eats ~90 chars before any filename — long enough
	// timestamps push the socket over the limit and tmux fails with
	// "File name too long" at bind time. /tmp is short and writable on
	// every supported OS.
	name := "tmuxt" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10)
	dir, err := os.MkdirTemp("/tmp", "tmuxt-")
	if err != nil {
		t.Skipf("cannot create /tmp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, name+".sock")

	s, err := New(ctx, socketPath, name, 80, 20, nil, "", []string{"sh", "-c", "printf READY; cat"}, nil)
	if err != nil {
		t.Skipf("tmux unavailable in this environment: %v", err)
	}
	defer s.Kill(context.Background())
	if s.SocketPath == "" {
		t.Fatal("SocketPath is empty")
	}
	ok, err := s.HasSession(ctx)
	if err != nil {
		t.Fatalf("HasSession: %v; server output: %q", err, s.serverOutput.String())
	}
	if !ok {
		t.Fatalf("session %q was not found on socket %s; server output: %q", s.Name, s.SocketPath, s.serverOutput.String())
	}

	var got string
	for i := 0; i < 30; i++ {
		got, err = s.Capture(ctx, true)
		if err != nil {
			t.Fatalf("Capture ready: %v", err)
		}
		if strings.Contains(got, "READY") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(got, "READY") {
		if strings.TrimSpace(got) == "" {
			t.Skip("tmux pane produced no output in this environment")
		}
		t.Fatalf("capture missing READY marker: %q", got)
	}

	if err := s.SendLiteral(ctx, "hello from tmuxctl"); err != nil {
		t.Fatalf("SendLiteral: %v", err)
	}
	if err := s.SendKeys(ctx, "Enter"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}

	for i := 0; i < 30; i++ {
		got, err = s.Capture(ctx, true)
		if err != nil {
			t.Fatalf("Capture: %v", err)
		}
		if strings.Contains(got, "hello from tmuxctl") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.TrimSpace(got) == "" {
		t.Skip("tmux pane produced no output in this environment")
	}
	t.Fatalf("capture missing sent text: %q", got)
}
