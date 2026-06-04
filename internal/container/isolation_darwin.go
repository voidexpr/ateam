package container

import (
	"errors"
	"os"
	"os/exec"
)

// detectOSSandbox probes for an outer macOS Seatbelt sandbox by
// attempting to apply a permissive nested profile. The kernel refuses
// nested sandbox_apply calls, so an exec exit error means we're
// already inside Seatbelt.
//
// Lookup / not-found errors (sandbox-exec or /usr/bin/true missing)
// are treated as "no detection" rather than a false-positive Seatbelt
// signal — a false positive there would silently drop the agent's
// inner sandbox with sandbox_detection=true.
//
// Measured at ~46ms on a 2024 Apple Silicon. Result is cached by
// cachedDetectOSSandbox so we pay it once per process.
func detectOSSandbox() string {
	cmd := exec.Command("/usr/bin/sandbox-exec",
		"-p", "(version 1)(allow default)", "/usr/bin/true")
	err := cmd.Run()
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) || os.IsNotExist(err) {
		return ""
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		if os.IsNotExist(execErr.Err) || errors.Is(execErr.Err, exec.ErrNotFound) {
			return ""
		}
	}
	return "macos:seatbelt"
}
