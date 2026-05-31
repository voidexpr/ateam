package actions

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// ============================================================
// CheckConcurrentRuns
// ============================================================

func TestCheckConcurrentRuns(t *testing.T) {
	setup := func(t *testing.T) (flow.RunCtx, *calldb.CallDB) {
		t.Helper()
		dbPath := filepath.Join(t.TempDir(), "state.sqlite")
		db, err := calldb.Open(dbPath)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { db.Close() })
		env := &root.ResolvedEnv{ProjectDir: filepath.Join(t.TempDir(), "proj-x")}
		rc := flow.RunCtx{
			Ctx:      context.Background(),
			DB:       db,
			Resolved: env,
			Reporter: flow.NoopReporter{},
		}
		return rc, db
	}

	t.Run("If=false → no-op even with live runs", func(t *testing.T) {
		rc, db := setup(t)
		seedLive(t, db, rc.Resolved.ProjectID(), "verify", "supervisor")
		f := CheckConcurrentRuns{If: false, Action: "verify"}.Run(rc, flow.RuntimeEnv{}, nil)
		if f.State != flow.StateContinue {
			t.Errorf("expected continue with If=false, got %v (%v)", f.State, f.Err)
		}
	})

	t.Run("nothing running → continue", func(t *testing.T) {
		rc, _ := setup(t)
		f := CheckConcurrentRuns{If: true, Action: "verify"}.Run(rc, flow.RuntimeEnv{}, nil)
		if f.State != flow.StateContinue {
			t.Errorf("expected continue with empty DB, got %v (%v)", f.State, f.Err)
		}
	})

	t.Run("live run for same action errors", func(t *testing.T) {
		rc, db := setup(t)
		seedLive(t, db, rc.Resolved.ProjectID(), "verify", "supervisor")
		f := CheckConcurrentRuns{If: true, Action: "verify"}.Run(rc, flow.RuntimeEnv{}, nil)
		if f.State != flow.StateError {
			t.Fatalf("expected error, got %v", f.State)
		}
		if !strings.Contains(f.Reason, "already running") {
			t.Errorf("expected 'already running' in reason: %q", f.Reason)
		}
	})

	t.Run("Action defaults from env.Action when empty", func(t *testing.T) {
		rc, db := setup(t)
		seedLive(t, db, rc.Resolved.ProjectID(), "verify", "supervisor")
		f := CheckConcurrentRuns{If: true}.Run(rc, flow.RuntimeEnv{Action: "verify"}, nil)
		if f.State != flow.StateError {
			t.Errorf("expected error when env.Action drives the check; got %v", f.State)
		}
	})

	t.Run("missing DB errors", func(t *testing.T) {
		rc := flow.RunCtx{
			Ctx:      context.Background(),
			Resolved: &root.ResolvedEnv{ProjectDir: "/x"},
			Reporter: flow.NoopReporter{},
		}
		f := CheckConcurrentRuns{If: true, Action: "verify"}.Run(rc, flow.RuntimeEnv{}, nil)
		if f.State != flow.StateError || !strings.Contains(f.Reason, "rc.DB is nil") {
			t.Errorf("expected nil-DB error, got state=%v reason=%q", f.State, f.Reason)
		}
	})

	t.Run("missing Resolved errors", func(t *testing.T) {
		rc := flow.RunCtx{
			Ctx:      context.Background(),
			DB:       openTempDB(t),
			Reporter: flow.NoopReporter{},
		}
		f := CheckConcurrentRuns{If: true, Action: "verify"}.Run(rc, flow.RuntimeEnv{}, nil)
		if f.State != flow.StateError || !strings.Contains(f.Reason, "rc.Resolved is nil") {
			t.Errorf("expected nil-Resolved error, got state=%v reason=%q", f.State, f.Reason)
		}
	})

	t.Run("scratch mode (no project, no org) → continue", func(t *testing.T) {
		rc := flow.RunCtx{
			Ctx:      context.Background(),
			DB:       openTempDB(t),
			Resolved: &root.ResolvedEnv{},
			Reporter: flow.NoopReporter{},
		}
		f := CheckConcurrentRuns{If: true, Action: "verify"}.Run(rc, flow.RuntimeEnv{}, nil)
		if f.State != flow.StateContinue {
			t.Errorf("scratch mode should continue; got %v (%v)", f.State, f.Err)
		}
	})
}

// ============================================================
// PrintArtifactPath
// ============================================================

func TestPrintArtifactPath(t *testing.T) {
	t.Run("emits Label: Path", func(t *testing.T) {
		got := captureStdout(t, func() {
			f := PrintArtifactPath{Label: "Verification", Path: "/tmp/x.md"}.
				Run(flow.RunCtx{}, flow.RuntimeEnv{}, nil)
			if f.State != flow.StateContinue {
				t.Errorf("state: %v", f.State)
			}
		})
		want := "Verification: /tmp/x.md\n"
		if got != want {
			t.Errorf("output: got %q want %q", got, want)
		}
	})

	t.Run("empty Label → no-op", func(t *testing.T) {
		got := captureStdout(t, func() {
			PrintArtifactPath{Path: "/tmp/x.md"}.Run(flow.RunCtx{}, flow.RuntimeEnv{}, nil)
		})
		if got != "" {
			t.Errorf("expected silent; got %q", got)
		}
	})

	t.Run("empty Path → no-op", func(t *testing.T) {
		got := captureStdout(t, func() {
			PrintArtifactPath{Label: "X"}.Run(flow.RunCtx{}, flow.RuntimeEnv{}, nil)
		})
		if got != "" {
			t.Errorf("expected silent; got %q", got)
		}
	})
}

// ============================================================
// PrintArtifactBody
// ============================================================

func TestPrintArtifactBody(t *testing.T) {
	t.Run("If=false → no-op", func(t *testing.T) {
		got := captureStdout(t, func() {
			PrintArtifactBody{If: false, Path: "/nonexistent"}.
				Run(flow.RunCtx{}, flow.RuntimeEnv{}, nil)
		})
		if got != "" {
			t.Errorf("If=false should be silent; got %q", got)
		}
	})

	t.Run("reads existing file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "report.md")
		if err := os.WriteFile(path, []byte("# Report\nHello\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := captureStdout(t, func() {
			PrintArtifactBody{If: true, Path: path}.Run(flow.RunCtx{}, flow.RuntimeEnv{}, nil)
		})
		if !strings.Contains(got, "# Report") || !strings.Contains(got, "Hello") {
			t.Errorf("output missing file contents: %q", got)
		}
	})

	t.Run("appends newline when file missing one", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "no-newline.md")
		if err := os.WriteFile(path, []byte("no trailing newline"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got := captureStdout(t, func() {
			PrintArtifactBody{If: true, Path: path}.Run(flow.RunCtx{}, flow.RuntimeEnv{}, nil)
		})
		if !strings.HasSuffix(got, "\n") {
			t.Errorf("expected trailing newline; got %q", got)
		}
	})

	t.Run("falls back to Summary.Output when file missing", func(t *testing.T) {
		res := &flow.Result{
			Summary: &runner.RunSummary{Output: "fallback body content"},
		}
		got := captureStdout(t, func() {
			PrintArtifactBody{If: true, Path: "/definitely/not/here.md"}.
				Run(flow.RunCtx{}, flow.RuntimeEnv{}, res)
		})
		if !strings.Contains(got, "fallback body content") {
			t.Errorf("expected fallback to Summary.Output; got %q", got)
		}
	})

	t.Run("falls back to Summary.Output when file empty", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.md")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		res := &flow.Result{Summary: &runner.RunSummary{Output: "from-stream"}}
		got := captureStdout(t, func() {
			PrintArtifactBody{If: true, Path: path}.Run(flow.RunCtx{}, flow.RuntimeEnv{}, res)
		})
		if !strings.Contains(got, "from-stream") {
			t.Errorf("expected fallback on empty file; got %q", got)
		}
	})
}

// ============================================================
// Helpers
// ============================================================

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

func openTempDB(t *testing.T) *calldb.CallDB {
	t.Helper()
	db, err := calldb.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedLive inserts an open agent_execs row (no EndedAt) so FindRunning
// returns it as a live process. PID is os.Getpid so the liveness check
// sees an active process. Mirrors internal/stage/actions/actions_test.go.
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
