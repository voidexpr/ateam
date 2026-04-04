//go:build windows

package cmd

import "os"

func stdoutWidth() int {
	return 0
}

func subscribeWindowResize() (<-chan os.Signal, func()) {
	return nil, func() {}
}
