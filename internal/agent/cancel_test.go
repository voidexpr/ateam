package agent

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestConfigureProcessLifecycle_KillsTreeOnCancel verifies that a process
// ignoring SIGTERM is killed by the WaitDelay-driven SIGKILL escalation,
// and that cmd.Wait returns within a bounded time after context cancel.
func TestConfigureProcessLifecycle_KillsTreeOnCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in -short mode")
	}

	// Shell that ignores SIGTERM and would otherwise sleep for 5 minutes.
	script := "trap '' TERM; sleep 300"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	configureProcessLifecycle(cmd)
	// Override the long default for this test so we don't actually wait 30s.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Cancel almost immediately; configureProcessLifecycle should send SIGTERM
	// to the group (ignored by trap), then exec's WaitDelay machinery should
	// escalate to SIGKILL after 2s.
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := cmd.Wait()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected Wait to return an error after cancel; got nil")
	}
	// Should die within (waitDelay + small slack), well below the 5-minute sleep.
	if elapsed > 5*time.Second {
		t.Errorf("process took %v to exit after cancel; expected ≤ ~3s (waitDelay 2s + slack)", elapsed)
	}
}

// TestConfigureProcessLifecycle_SetsPgid verifies the subprocess is its own
// process-group leader so signals to -PID reach the whole tree.
func TestConfigureProcessLifecycle_SetsPgid(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in -short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 30")
	configureProcessLifecycle(cmd)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	}()

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("Getpgid: %v", err)
	}
	if pgid != cmd.Process.Pid {
		t.Errorf("PGID = %d, want PID %d (subprocess should be its own group leader)", pgid, cmd.Process.Pid)
	}
}
