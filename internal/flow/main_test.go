package flow

import (
	"os"
	"testing"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/testenv"
)

// TestMain keeps discovery below the temp root and HOME on an empty
// directory so the developer's ~/.ateam never leaks into tests.
func TestMain(m *testing.M) {
	root.SetDiscoveryCeiling(os.TempDir())
	os.Exit(testenv.RunWithTempHome(m))
}
