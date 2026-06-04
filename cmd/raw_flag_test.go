package cmd

import "testing"

// TestRawFlagsRegisteredOnExecAndParallel ensures the `--raw` flag is
// present on `ateam exec` and `ateam parallel` even though it's a no-op
// until the runner-side ALL_CAPS → dotted substitution migration lands.
// Catches accidental removal during refactors of the cmd's flag init.
func TestRawFlagsRegisteredOnExecAndParallel(t *testing.T) {
	if execCmd.Flags().Lookup("raw") == nil {
		t.Error("exec --raw flag missing")
	}
	if parallelCmd.Flags().Lookup("raw") == nil {
		t.Error("parallel --raw flag missing")
	}
}
