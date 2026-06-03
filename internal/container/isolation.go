package container

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

// IsolationSource returns a short identifier describing why ateam thinks
// it is already isolated (Docker/Podman container, fence, firejail,
// macOS Seatbelt, or a Linux user namespace), or "" if no isolation
// was detected.
//
// Detection has three layers:
//
//  1. Always-on: explicit ateam overrides (ATEAM_IN_CONTAINER=1,
//     ATEAM_IN_SANDBOX=1). Cannot be disabled.
//
//  2. Docker layer: /.dockerenv (Docker) and /run/.containerenv
//     (Podman). Gated by DockerDetectionEnabled (default true). These
//     markers are extremely reliable; the toggle exists for symmetry
//     and edge cases (e.g. testing the host-execution code path from
//     inside a container).
//
//  3. Sandbox layer: cooperative env vars (FENCE_SANDBOX, FIREJAIL_NAME,
//     container=...) and the per-OS probes (macOS sandbox-exec test,
//     Linux user-namespace / Seccomp / NoNewPrivs). Gated by
//     SandboxDetectionEnabled (default false). These signals can have
//     false positives — e.g. systemd-hardened user services trip the
//     Linux probe without being inside an outer sandbox — so the
//     conservative default is off. Set sandbox_detection = true in
//     runtime.hcl (or pass --sandbox-detection=true) when running
//     ateam under fence, firejail, or similar.
//
// Result is computed once per process and cached.
func IsolationSource() string {
	isoOnce.Do(func() { isoSource = detectIsolation() })
	return isoSource
}

var (
	isoOnce   sync.Once
	isoSource string
)

func detectIsolation() string {
	if s := detectAlwaysOn(); s != "" {
		return s
	}
	if DockerDetectionEnabled() {
		if s := detectDockerMarkers(); s != "" {
			return s
		}
	}
	if SandboxDetectionEnabled() {
		if s := detectSandboxLayer(); s != "" {
			return s
		}
	}
	return ""
}

func detectAlwaysOn() string {
	if os.Getenv("ATEAM_IN_CONTAINER") == "1" {
		return "env:ATEAM_IN_CONTAINER"
	}
	if os.Getenv("ATEAM_IN_SANDBOX") == "1" {
		return "env:ATEAM_IN_SANDBOX"
	}
	return ""
}

// DockerMarker returns the marker file path that signals ateam is
// inside a Docker/Podman container (/.dockerenv or /run/.containerenv),
// or "" if no marker is present. Independent of DockerDetectionEnabled
// — callers that need the "would the toggle apply this?" answer should
// also consult that helper.
func DockerMarker() string {
	for _, m := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(m); err == nil {
			return m
		}
	}
	return ""
}

func detectDockerMarkers() string {
	if m := DockerMarker(); m != "" {
		return "marker:" + m
	}
	return ""
}

// detectSandboxLayer runs the cooperative-env-var and per-OS probes
// for non-container outer isolation (fence, firejail, Seatbelt, bwrap).
// Called both by detectIsolation (when SandboxDetectionEnabled is true)
// and by IsInSandbox (regardless of the toggle — for warning callsites).
//
// Env-var checks are free; the OS probe is cached because on macOS it
// spawns sandbox-exec (~46ms) and the result depends only on the
// running process's outer state, which doesn't change.
func detectSandboxLayer() string {
	if os.Getenv("FENCE_SANDBOX") != "" {
		return "fence"
	}
	if os.Getenv("FIREJAIL_NAME") != "" {
		return "firejail"
	}
	if v := os.Getenv("container"); v != "" {
		return "container=" + v
	}
	return cachedDetectOSSandbox()
}

func cachedDetectOSSandbox() string {
	osSandboxOnce.Do(func() { osSandboxResult = detectOSSandbox() })
	return osSandboxResult
}

var (
	osSandboxOnce   sync.Once
	osSandboxResult string
)

// IsInDockerContainer returns true when ateam can prove it is inside a
// Docker / Podman container — either by a marker file or an explicit
// ATEAM_IN_CONTAINER=1 override. Unlike IsInContainer, this does NOT
// fire on sandbox-layer detection (fence, Seatbelt, bwrap), so it is
// safe to gate code that genuinely requires container semantics
// (e.g. `ateam claude`, which only works on Linux inside a container).
func IsInDockerContainer() bool {
	if os.Getenv("ATEAM_IN_CONTAINER") == "1" {
		return true
	}
	return DockerMarker() != ""
}

// IsolationCategory classifies the active IsolationSource into one of
// "env", "docker", "sandbox", or "" if nothing is detected. Use this
// instead of pattern-matching the source string yourself.
func IsolationCategory() string {
	src := IsolationSource()
	switch {
	case src == "":
		return ""
	case strings.HasPrefix(src, "env:"):
		return "env"
	case strings.HasPrefix(src, "marker:"):
		return "docker"
	default:
		return "sandbox"
	}
}

// IsInSandbox runs the sandbox-layer detection probes regardless of
// SandboxDetectionEnabled. Use for callsites that need to know
// "ateam probably is inside an outer sandbox" even when ateam has been
// told (via the toggle) not to act on the detection.
//
// Always-on ATEAM_IN_SANDBOX=1 also counts. ATEAM_IN_CONTAINER and
// Docker markers do NOT count — those are container-side signals, not
// sandbox-side, and they don't predict failure of an inner agent
// sandbox the way an outer Seatbelt / bwrap does.
//
// Returns (true, source) when something fires, (false, "") otherwise.
func IsInSandbox() (bool, string) {
	if os.Getenv("ATEAM_IN_SANDBOX") == "1" {
		return true, "env:ATEAM_IN_SANDBOX"
	}
	if s := detectSandboxLayer(); s != "" {
		return true, s
	}
	return false, ""
}

// WarnIfInSandbox prints a one-time stderr warning when ateam appears
// to be inside an outer sandbox and is about to apply isolation that
// usually doesn't nest cleanly (the agent's inner sandbox, or a
// container). The action string is a short verb phrase describing what
// ateam is about to do.
//
// Suppressed when nothing is detected. Suppressed after the first call
// in this process (the failure mode the warning describes is
// process-global, not per-action — repeating it on every agent exec
// would just be noise).
func WarnIfInSandbox(action string) {
	yes, src := IsInSandbox()
	if !yes {
		return
	}
	warnOnce.Do(func() {
		fmt.Fprintf(os.Stderr,
			"Warning: ateam appears to be inside an outer sandbox (source: %s — could be\n"+
				"  macOS Seatbelt, Linux bubblewrap/firejail, fence, or anything else that\n"+
				"  trips the same signals) but is about to %s. Nested isolation usually fails.\n"+
				"  If it does, set sandbox_detection = true in runtime.hcl (or pass\n"+
				"  --sandbox-detection=true) so ateam treats the outer sandbox as a container\n"+
				"  and skips the agent's inner sandbox.\n",
			src, action)
	})
}

var warnOnce sync.Once

// SandboxDetectionEnabled reports whether ateam should let the
// sandbox-layer detection (cooperative env vars + per-OS probes)
// influence IsolationSource. Default is false — these signals can
// have false positives, and a false positive silently drops the
// agent's inner sandbox. Override via runtime.hcl
// (sandbox_detection = true) or the --sandbox-detection CLI flag when
// running ateam under fence, firejail, or similar.
func SandboxDetectionEnabled() bool {
	if v := sandboxDetectionOverride.Load(); v != nil {
		return *v
	}
	return false
}

// DockerDetectionEnabled reports whether ateam should let the Docker
// marker files (/.dockerenv, /run/.containerenv) influence
// IsolationSource. Default is true — these markers are dead-reliable.
// The toggle exists for symmetry with sandbox_detection and for edge
// cases (testing the host-execution code path from inside a container).
func DockerDetectionEnabled() bool {
	if v := dockerDetectionOverride.Load(); v != nil {
		return *v
	}
	return true
}

// SetSandboxDetection overrides the default. Call early — before the
// first IsolationSource call — so the cached result reflects the
// override.
func SetSandboxDetection(enabled bool) {
	v := enabled
	sandboxDetectionOverride.Store(&v)
}

// SetDockerDetection — symmetric to SetSandboxDetection.
func SetDockerDetection(enabled bool) {
	v := enabled
	dockerDetectionOverride.Store(&v)
}

// SetSandboxDetectionIfUnset behaves like SetSandboxDetection but
// no-ops if a previous Set has happened (e.g. by CLI). Used to apply
// runtime.hcl values without overriding the CLI.
func SetSandboxDetectionIfUnset(enabled bool) {
	if sandboxDetectionOverride.Load() != nil {
		return
	}
	SetSandboxDetection(enabled)
}

// SetDockerDetectionIfUnset — symmetric to SetSandboxDetectionIfUnset.
func SetDockerDetectionIfUnset(enabled bool) {
	if dockerDetectionOverride.Load() != nil {
		return
	}
	SetDockerDetection(enabled)
}

// ResetDetectionForTest clears every override (sandbox and docker),
// the cached IsolationSource result, the cached OS-probe result, and
// the WarnIfInSandbox once-guard. Intended for tests only.
func ResetDetectionForTest() {
	sandboxDetectionOverride.Store(nil)
	dockerDetectionOverride.Store(nil)
	isoOnce = sync.Once{}
	isoSource = ""
	warnOnce = sync.Once{}
	osSandboxOnce = sync.Once{}
	osSandboxResult = ""
}

var (
	sandboxDetectionOverride atomic.Pointer[bool]
	dockerDetectionOverride  atomic.Pointer[bool]
)
