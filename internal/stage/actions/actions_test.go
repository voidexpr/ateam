package actions

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/stage"
)

// captureStdout redirects os.Stdout while fn runs and returns the bytes
// written. Mirrors the cmd-package helper since we're in a different
// package.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = pw
	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		io.Copy(&buf, pr)
		close(done)
	}()
	fn()
	pw.Close()
	os.Stdout = old
	<-done
	return buf.String()
}

func TestFailOnExecError(t *testing.T) {
	t.Run("nil error → no-op", func(t *testing.T) {
		c := &stage.Ctx{Result: &runner.RunSummary{}}
		if err := (FailOnExecError{Label: "verify"}.Run(c)); err != nil {
			t.Errorf("expected nil, got %v", err)
		}
	})
	t.Run("non-nil error wraps with label", func(t *testing.T) {
		boom := errors.New("boom")
		c := &stage.Ctx{Result: &runner.RunSummary{Err: boom}}
		err := FailOnExecError{Label: "verify"}.Run(c)
		if !errors.Is(err, boom) {
			t.Errorf("error chain missing boom: %v", err)
		}
		if !strings.Contains(err.Error(), "verify failed") {
			t.Errorf("expected 'verify failed' label, got %v", err)
		}
	})
	t.Run("missing label falls back to generic", func(t *testing.T) {
		boom := errors.New("boom")
		c := &stage.Ctx{Result: &runner.RunSummary{Err: boom}}
		err := FailOnExecError{}.Run(c)
		if !strings.Contains(err.Error(), "agent run failed") {
			t.Errorf("expected default label, got %v", err)
		}
	})
	t.Run("nil Result errors", func(t *testing.T) {
		if err := (FailOnExecError{}.Run(&stage.Ctx{})); err == nil {
			t.Error("expected error for nil Result")
		}
	})
}

func TestPrintDone(t *testing.T) {
	c := &stage.Ctx{Result: &runner.RunSummary{
		Duration: 90 * time.Second,
		Cost:     0.42,
	}}
	out := captureStdout(t, func() {
		if err := (PrintDone{}.Run(c)); err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	if !strings.Contains(out, "Done (") {
		t.Errorf("missing 'Done (' in output: %q", out)
	}
	if !strings.Contains(out, "$0.42") {
		t.Errorf("missing cost in output: %q", out)
	}
}

func TestPrintArtifactPath(t *testing.T) {
	t.Run("emits label: path", func(t *testing.T) {
		c := &stage.Ctx{}
		out := captureStdout(t, func() {
			PrintArtifactPath{Label: "Review", Path: "/tmp/r.md"}.Run(c)
		})
		if out != "Review: /tmp/r.md\n" {
			t.Errorf("output = %q", out)
		}
	})
	t.Run("empty label or path → no-op", func(t *testing.T) {
		c := &stage.Ctx{}
		out := captureStdout(t, func() {
			PrintArtifactPath{}.Run(c)
			PrintArtifactPath{Label: "x"}.Run(c)
			PrintArtifactPath{Path: "/y"}.Run(c)
		})
		if out != "" {
			t.Errorf("expected no output, got %q", out)
		}
	})
}

func TestPrintArtifactBody(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "v.md")
	const body = "# Real Body\nfindings\n"
	if err := os.WriteFile(filePath, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.md")

	t.Run("If=false → no-op", func(t *testing.T) {
		c := &stage.Ctx{Result: &runner.RunSummary{Output: "STREAM"}}
		out := captureStdout(t, func() {
			PrintArtifactBody{Path: filePath}.Run(c)
		})
		if out != "" {
			t.Errorf("expected no output with If=false, got %q", out)
		}
	})
	t.Run("file present wins over stream", func(t *testing.T) {
		c := &stage.Ctx{Result: &runner.RunSummary{Output: "STREAM-FALLBACK"}}
		out := captureStdout(t, func() {
			PrintArtifactBody{If: true, Path: filePath}.Run(c)
		})
		if !strings.Contains(out, "Real Body") {
			t.Errorf("missing file body in output: %q", out)
		}
		if strings.Contains(out, "STREAM-FALLBACK") {
			t.Errorf("should not have printed stream when file present: %q", out)
		}
	})
	t.Run("missing file falls back to stream", func(t *testing.T) {
		c := &stage.Ctx{Result: &runner.RunSummary{Output: "STREAM-FALLBACK"}}
		out := captureStdout(t, func() {
			PrintArtifactBody{If: true, Path: missing}.Run(c)
		})
		if !strings.Contains(out, "STREAM-FALLBACK") {
			t.Errorf("expected stream fallback, got %q", out)
		}
	})
}

func TestCheckConcurrentRuns(t *testing.T) {
	setup := func(t *testing.T) (*stage.Ctx, *calldb.CallDB) {
		t.Helper()
		dbPath := filepath.Join(t.TempDir(), "state.sqlite")
		db, err := calldb.Open(dbPath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		// Stand up an env whose ProjectID resolves to a non-empty value
		// without an org dir. The empty-projectID guards take the
		// scratch-mode path otherwise.
		env := &root.ResolvedEnv{ProjectDir: filepath.Join(t.TempDir(), "proj-x")}
		return &stage.Ctx{Env: env, DB: db}, db
	}

	t.Run("If=false → no-op even with live runs", func(t *testing.T) {
		c, db := setup(t)
		seedLive(t, db, c.Env.ProjectID(), "verify", "supervisor")
		err := CheckConcurrentRuns{If: false, Action: "verify"}.Run(c)
		if err != nil {
			t.Errorf("expected no error with If=false, got %v", err)
		}
	})
	t.Run("nothing running → ok", func(t *testing.T) {
		c, _ := setup(t)
		err := CheckConcurrentRuns{If: true, Action: "verify"}.Run(c)
		if err != nil {
			t.Errorf("expected no error with empty DB, got %v", err)
		}
	})
	t.Run("live run for same action errors", func(t *testing.T) {
		c, db := setup(t)
		seedLive(t, db, c.Env.ProjectID(), "verify", "supervisor")
		err := CheckConcurrentRuns{If: true, Action: "verify"}.Run(c)
		if err == nil {
			t.Fatal("expected error from live run, got nil")
		}
		if !strings.Contains(err.Error(), "already running") {
			t.Errorf("expected 'already running' in error, got %v", err)
		}
	})
	t.Run("missing DB errors", func(t *testing.T) {
		c := &stage.Ctx{Env: &root.ResolvedEnv{ProjectDir: "/x"}}
		err := CheckConcurrentRuns{If: true, Action: "verify"}.Run(c)
		if err == nil || !strings.Contains(err.Error(), "Ctx.DB is nil") {
			t.Errorf("expected nil-DB error, got %v", err)
		}
	})
}

// seedLive inserts an open agent_execs row (no EndedAt) so FindRunning
// returns it as a live process. The PID is set to our own process so
// the liveness check sees an active process.
func seedLive(t *testing.T, db *calldb.CallDB, projectID, action, role string) {
	t.Helper()
	id, err := db.InsertCall(&calldb.Call{
		ProjectID: projectID,
		Action:    action,
		Role:      role,
		Batch:     fmt.Sprintf("%s-test", action),
		StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.SetPID(id, os.Getpid(), ""); err != nil {
		t.Fatalf("SetPID: %v", err)
	}
}
