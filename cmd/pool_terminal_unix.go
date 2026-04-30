//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// stdoutSize returns the column and row count of stdout's terminal. Either
// value is 0 when not a TTY or the size cannot be determined. The pool
// status redraw needs both — bundling them into one ioctl halves syscalls
// on the redraw hot path.
func stdoutSize() (cols, rows int) {
	if !isTerminal() {
		return 0, 0
	}
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws == nil {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
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
