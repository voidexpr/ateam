//go:build windows

package cmd

import "os"

func stdoutSize() (cols, rows int) {
	return 0, 0
}

func subscribeWindowResize() (<-chan os.Signal, func()) {
	return nil, func() {}
}
