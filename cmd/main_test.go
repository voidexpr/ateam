package cmd

import (
	"os"
	"testing"

	"github.com/ateam/internal/container"
)

// TestMain disables the toggleable sandbox-detection layer during cmd
// tests. macOS test runners are often themselves under Seatbelt (e.g.
// invoked inside Claude Code's sandbox), which is a true positive but
// flips buildRunner into the in-container code path and breaks tests
// that assume host execution. Explicit container markers (/.dockerenv,
// ATEAM_IN_CONTAINER=1) still trigger because they live in the
// always-on layer.
func TestMain(m *testing.M) {
	container.SetSandboxDetection(false)
	os.Exit(m.Run())
}
