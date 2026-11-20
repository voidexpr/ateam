// Package testenv isolates package tests from the developer's machine.
// It depends only on the standard library so any package's TestMain can
// use it without an import cycle.
package testenv

import (
	"fmt"
	"os"
	"testing"
)

// RunWithTempHome runs m with HOME pointed at a fresh empty directory, so
// tests never read the developer's real ~/.ateam, agent config or auth, and
// temp paths are not displayed as ~/... when $TMPDIR lives under the real
// home. Use from TestMain: os.Exit(testenv.RunWithTempHome(m)).
func RunWithTempHome(m *testing.M) int {
	home, err := os.MkdirTemp("", "ateam-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: create temp HOME: %v\n", err)
		return 1
	}
	defer os.RemoveAll(home)
	if err := os.Setenv("HOME", home); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: set HOME: %v\n", err)
		return 1
	}
	return m.Run()
}
