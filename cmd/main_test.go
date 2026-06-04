package cmd

import (
	"os"
	"testing"

	"github.com/ateam/internal/container"
)

// TestMain disables both toggleable detection layers during cmd tests.
// macOS test runners are often themselves under Seatbelt (e.g. invoked
// inside Claude Code's sandbox), which is a true positive but flips
// buildRunner into the in-container code path and breaks tests that
// assume host execution. The same flip happens under nested Docker
// (make test-docker) where /.dockerenv is present. Disable both
// toggled layers; the always-on env-var overrides (ATEAM_IN_CONTAINER=1,
// ATEAM_IN_SANDBOX=1) still trigger for tests that need them.
func TestMain(m *testing.M) {
	container.SetSandboxDetection(false)
	container.SetDockerDetection(false)
	os.Exit(m.Run())
}
