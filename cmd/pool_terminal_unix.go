//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func stdoutWidth() int {
	if !isTerminal() {
		return 0
	}
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws == nil || ws.Col == 0 {
		return 0
	}
	return int(ws.Col)
}

func subscribeWindowResize() (<-chan os.Signal, func()) {
	if !isTerminal() {
		return nil, func() {}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	return ch, func() {
		signal.Stop(ch)
		close(ch)
	}
}
