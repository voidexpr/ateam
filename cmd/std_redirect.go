package cmd

import (
	"io"
	"os"
	"sync"
)

// stdRedirect captures process-wide writes to os.Stdout and os.Stderr
// and drains them through sink so the live renderer's interleaving
// writer can scroll them above the status table instead of having them
// punch through the cursor accounting.
//
// Why this is the durable fix: hunting individual fmt.Fprintf(os.Stderr, …)
// call sites is whack-a-mole — every new contributor (or library)
// reintroduces the bug. Redirecting the os.Stdout / os.Stderr variables
// catches every write that goes through Go's standard library
// (`fmt.Println`, `log.Printf`, `print`, `panic`, …). Subprocess
// stdout/stderr is unaffected because agents capture those via
// cmd.StdoutPipe / setupStreamFiles before they reach the parent's
// fds. Direct syscall.Write(1, …) would also bypass this, but Go code
// doesn't normally do that.
//
// Caller is responsible for ensuring the renderer remains alive until
// Restore returns — drain goroutines write into sink, so closing the
// renderer first would race with in-flight bytes. runPool's defer
// ordering (Restore before renderer.Close) guarantees this.
type stdRedirect struct {
	origStdout, origStderr *os.File
	stdoutW, stderrW       *os.File
	stdoutR, stderrR       *os.File
	drainDone              sync.WaitGroup
}

// redirectStdStreams replaces os.Stdout and os.Stderr with pipes that
// drain into sink. Returns a restore func that puts the originals back
// and waits for any pending bytes to drain. If pipe creation fails the
// returned func is a no-op and the originals are left in place.
func redirectStdStreams(sink io.Writer) func() {
	r := &stdRedirect{
		origStdout: os.Stdout,
		origStderr: os.Stderr,
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return func() {}
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return func() {}
	}

	r.stdoutR, r.stdoutW = stdoutR, stdoutW
	r.stderrR, r.stderrW = stderrR, stderrW
	os.Stdout = stdoutW
	os.Stderr = stderrW

	r.drainDone.Add(2)
	go func() {
		defer r.drainDone.Done()
		_, _ = io.Copy(sink, stdoutR)
		_ = stdoutR.Close()
	}()
	go func() {
		defer r.drainDone.Done()
		_, _ = io.Copy(sink, stderrR)
		_ = stderrR.Close()
	}()

	return r.restore
}

func (r *stdRedirect) restore() {
	os.Stdout = r.origStdout
	os.Stderr = r.origStderr
	// Closing the write ends causes the readers to see EOF, which lets
	// the io.Copy goroutines return. Wait for them so any final bytes
	// are flushed into sink before the caller closes the renderer.
	if r.stdoutW != nil {
		_ = r.stdoutW.Close()
	}
	if r.stderrW != nil {
		_ = r.stderrW.Close()
	}
	r.drainDone.Wait()
}
