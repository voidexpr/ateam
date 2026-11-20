package runtime

import (
	"os"
	"testing"

	"github.com/ateam/internal/testenv"
)

// TestMain points HOME at an empty directory so displayed config paths do
// not depend on whether $TMPDIR lives under the developer's home.
func TestMain(m *testing.M) {
	os.Exit(testenv.RunWithTempHome(m))
}
