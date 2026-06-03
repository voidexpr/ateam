package container

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
)

// withCleanIsolation isolates the test from any real outer sandbox or
// caller-set state. It clears every env var the detector reads, clears
// the override + cache, runs fn, and restores everything in Cleanup.
func withCleanIsolation(t *testing.T, fn func()) {
	t.Helper()

	keys := []string{
		"ATEAM_IN_CONTAINER",
		"ATEAM_IN_SANDBOX",
		"FENCE_SANDBOX",
		"FIREJAIL_NAME",
		"container",
	}
	saved := make(map[string]string, len(keys))
	hadKey := make(map[string]bool, len(keys))
	for _, k := range keys {
		v, ok := os.LookupEnv(k)
		saved[k] = v
		hadKey[k] = ok
		_ = os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range keys {
			if hadKey[k] {
				_ = os.Setenv(k, saved[k])
			} else {
				_ = os.Unsetenv(k)
			}
		}
		ResetDetectionForTest()
	})

	ResetDetectionForTest()
	fn()
}

// captureStderr replaces os.Stderr for the duration of fn and returns
// what was written.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	<-done
	return buf.String()
}

// ── Always-on layer ────────────────────────────────────────────────

func TestExplicitAteamInContainerOverridesAll(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("ATEAM_IN_CONTAINER", "1")
		SetDockerDetection(false)  // gated layer off — explicit still wins
		SetSandboxDetection(false) //   same

		if got := IsolationSource(); got != "env:ATEAM_IN_CONTAINER" {
			t.Fatalf("want env:ATEAM_IN_CONTAINER, got %q", got)
		}
		if !IsInContainer() {
			t.Fatal("IsInContainer should be true under explicit override")
		}
	})
}

func TestExplicitAteamInSandboxOverridesAll(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("ATEAM_IN_SANDBOX", "1")
		SetDockerDetection(false)
		SetSandboxDetection(false)

		if got := IsolationSource(); got != "env:ATEAM_IN_SANDBOX" {
			t.Fatalf("want env:ATEAM_IN_SANDBOX, got %q", got)
		}
	})
}

// ── Sandbox layer (default OFF) ────────────────────────────────────

func TestSandboxLayerIsOffByDefault(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("FENCE_SANDBOX", "1")
		// Default: sandbox_detection = false. FENCE_SANDBOX should NOT
		// trigger IsolationSource (the agent will try to apply its
		// inner sandbox, which is the safe failure mode).
		if got := IsolationSource(); got == "fence" {
			t.Fatalf("with default detection off, fence should not trigger IsolationSource, got %q", got)
		}
	})
}

func TestSandboxLayerFiresWhenEnabled(t *testing.T) {
	withCleanIsolation(t, func() {
		// Disable the docker layer so /.dockerenv (e.g. inside the
		// docker-in-docker test environment) doesn't pre-empt the
		// sandbox layer we want to exercise.
		SetDockerDetection(false)
		SetSandboxDetection(true)
		t.Setenv("FENCE_SANDBOX", "1")
		if got := IsolationSource(); got != "fence" {
			t.Fatalf("want fence, got %q", got)
		}
	})
}

func TestFirejailEnvTriggersWhenEnabled(t *testing.T) {
	withCleanIsolation(t, func() {
		SetDockerDetection(false) // see TestSandboxLayerFiresWhenEnabled
		SetSandboxDetection(true)
		t.Setenv("FIREJAIL_NAME", "x")
		if got := IsolationSource(); got != "firejail" {
			t.Fatalf("want firejail, got %q", got)
		}
	})
}

func TestSystemdContainerEnvVar(t *testing.T) {
	withCleanIsolation(t, func() {
		SetDockerDetection(false) // see TestSandboxLayerFiresWhenEnabled
		SetSandboxDetection(true)
		t.Setenv("container", "docker")
		if got := IsolationSource(); got != "container=docker" {
			t.Fatalf("want container=docker, got %q", got)
		}
	})
}

// ── Toggle defaults + setters ──────────────────────────────────────

func TestSandboxDetectionDefaultIsFalse(t *testing.T) {
	withCleanIsolation(t, func() {
		if SandboxDetectionEnabled() {
			t.Fatal("sandbox_detection should default to false (safer; user opts in)")
		}
		SetSandboxDetection(true)
		if !SandboxDetectionEnabled() {
			t.Fatal("after SetSandboxDetection(true), should be enabled")
		}
	})
}

func TestDockerDetectionDefaultIsTrue(t *testing.T) {
	withCleanIsolation(t, func() {
		if !DockerDetectionEnabled() {
			t.Fatal("docker_detection should default to true (markers are reliable)")
		}
		SetDockerDetection(false)
		if DockerDetectionEnabled() {
			t.Fatal("after SetDockerDetection(false), should be disabled")
		}
	})
}

func TestSetSandboxDetectionIfUnsetRespectsPriorCall(t *testing.T) {
	withCleanIsolation(t, func() {
		SetSandboxDetection(true)
		SetSandboxDetectionIfUnset(false) // should be a no-op
		if !SandboxDetectionEnabled() {
			t.Fatal("SetSandboxDetectionIfUnset must not override an explicit prior Set")
		}
	})
}

func TestSetDockerDetectionIfUnsetRespectsPriorCall(t *testing.T) {
	withCleanIsolation(t, func() {
		SetDockerDetection(false)
		SetDockerDetectionIfUnset(true) // should be a no-op
		if DockerDetectionEnabled() {
			t.Fatal("SetDockerDetectionIfUnset must not override an explicit prior Set")
		}
	})
}

// ── IsInSandbox ────────────────────────────────────────────────────

func TestIsInSandboxIgnoresToggle(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("FENCE_SANDBOX", "1")
		SetSandboxDetection(false) // toggle is off

		// IsolationSource respects the toggle and returns "".
		if got := IsolationSource(); got == "fence" {
			t.Fatalf("IsolationSource should respect the toggle, got %q", got)
		}
		// IsInSandbox does NOT respect the toggle.
		yes, src := IsInSandbox()
		if !yes || src != "fence" {
			t.Fatalf("IsInSandbox should fire regardless of toggle: yes=%v src=%q", yes, src)
		}
	})
}

func TestIsInSandboxExplicitEnv(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("ATEAM_IN_SANDBOX", "1")
		yes, src := IsInSandbox()
		if !yes || src != "env:ATEAM_IN_SANDBOX" {
			t.Fatalf("yes=%v src=%q", yes, src)
		}
	})
}

func TestIsInSandboxDoesNotFireOnDockerMarkers(t *testing.T) {
	// /.dockerenv may or may not exist on the host; we can't write to
	// /. Just verify that ATEAM_IN_CONTAINER doesn't make IsInSandbox
	// fire (it's a container signal, not a sandbox signal).
	withCleanIsolation(t, func() {
		t.Setenv("ATEAM_IN_CONTAINER", "1")
		// Need a host where sandbox-layer probes return "", otherwise
		// we can't tell ATEAM_IN_CONTAINER didn't trigger. The macOS
		// CI runner does run under Seatbelt, so skip on darwin.
		if hostFiresSandboxProbe() {
			t.Skip("host already triggers sandbox-layer probe; can't isolate ATEAM_IN_CONTAINER effect")
		}
		if yes, _ := IsInSandbox(); yes {
			t.Fatal("IsInSandbox should NOT fire on ATEAM_IN_CONTAINER alone")
		}
	})
}

// hostFiresSandboxProbe reports whether the per-OS probe alone (without
// any cooperative env var) returns non-empty on this host. Used to
// skip tests that can't distinguish the contribution of a specific
// signal when the host itself is sandboxed.
func hostFiresSandboxProbe() bool {
	return detectOSSandbox() != ""
}

// ── WarnIfInSandbox ────────────────────────────────────────────────

func TestWarnIfInSandboxPrintsWhenDetected(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("FENCE_SANDBOX", "1")
		// Reset the warn-once.
		ResetDetectionForTest()
		t.Setenv("FENCE_SANDBOX", "1")

		out := captureStderr(t, func() {
			WarnIfInSandbox("apply the agent's inner sandbox")
		})
		if !bytes.Contains([]byte(out), []byte("fence")) {
			t.Fatalf("warning should mention source 'fence', got: %s", out)
		}
		if !bytes.Contains([]byte(out), []byte("apply the agent's inner sandbox")) {
			t.Fatalf("warning should mention the action, got: %s", out)
		}
	})
}

func TestWarnIfInSandboxSilentWhenNothingDetected(t *testing.T) {
	withCleanIsolation(t, func() {
		if hostFiresSandboxProbe() {
			t.Skip("host triggers sandbox-layer probe; can't observe silent path")
		}
		out := captureStderr(t, func() {
			WarnIfInSandbox("apply sandbox")
		})
		if out != "" {
			t.Fatalf("expected silent path, got: %s", out)
		}
	})
}

func TestWarnIfInSandboxFiresOnce(t *testing.T) {
	withCleanIsolation(t, func() {
		t.Setenv("FENCE_SANDBOX", "1")
		ResetDetectionForTest()
		t.Setenv("FENCE_SANDBOX", "1")

		// Fire twice from concurrent goroutines; expect one message.
		var wg sync.WaitGroup
		var buf bytes.Buffer
		orig := os.Stderr
		r, w, _ := os.Pipe()
		os.Stderr = w

		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(&buf, r)
			close(done)
		}()

		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				WarnIfInSandbox("apply sandbox")
			}()
		}
		wg.Wait()
		_ = w.Close()
		os.Stderr = orig
		<-done

		out := buf.String()
		warnings := bytes.Count([]byte(out), []byte("Warning:"))
		if warnings != 1 {
			t.Fatalf("expected exactly 1 warning, got %d: %s", warnings, out)
		}
	})
}
