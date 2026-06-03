package container

import "os/exec"

// detectOSSandbox probes for an outer macOS Seatbelt sandbox by
// attempting to apply a permissive nested profile. The kernel refuses
// nested sandbox_apply calls, so a non-nil error means we're already
// inside Seatbelt (fence, sandbox-exec, or any other Seatbelt user).
//
// Measured at ~46ms on a 2024 Apple Silicon. Result is cached by
// IsolationSource so we pay it once per process.
func detectOSSandbox() string {
	cmd := exec.Command("sandbox-exec",
		"-p", "(version 1)(allow default)", "/usr/bin/true")
	if err := cmd.Run(); err != nil {
		return "macos:seatbelt"
	}
	return ""
}
