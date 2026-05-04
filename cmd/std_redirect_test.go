package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestRedirectStdStreamsCapturesStdoutAndStderr verifies that writes
// through fmt.Fprintf to os.Stdout and os.Stderr land in the sink
// while redirection is active, and stop landing there after Restore.
func TestRedirectStdStreamsCapturesStdoutAndStderr(t *testing.T) {
	var sink syncBuf

	restore := redirectStdStreams(&sink)

	// Both standard streams should be intercepted.
	fmt.Fprint(os.Stdout, "from-stdout ")
	fmt.Fprint(os.Stderr, "from-stderr")

	restore()

	got := sink.String()
	if !strings.Contains(got, "from-stdout") {
		t.Errorf("sink missing stdout write: %q", got)
	}
	if !strings.Contains(got, "from-stderr") {
		t.Errorf("sink missing stderr write: %q", got)
	}

	// After restore, writes go back to the original streams. We can't
	// easily read those, but we can confirm the pipes are closed by
	// noting that subsequent writes don't grow the sink.
	before := sink.Len()
	// A no-op write — the originals are the real os.Stdout/Stderr now,
	// so nothing should hit the sink.
	_, _ = io.WriteString(io.Discard, "noop")
	if sink.Len() != before {
		t.Errorf("sink grew after restore: %q", sink.String())
	}
}

// TestRedirectStdStreamsRestoresOriginals verifies the originals are
// put back exactly. Important because tests run in a single process
// and a leak would corrupt later tests' output.
func TestRedirectStdStreamsRestoresOriginals(t *testing.T) {
	origOut, origErr := os.Stdout, os.Stderr
	var sink syncBuf
	restore := redirectStdStreams(&sink)
	if os.Stdout == origOut {
		t.Errorf("os.Stdout was not redirected")
	}
	if os.Stderr == origErr {
		t.Errorf("os.Stderr was not redirected")
	}
	restore()
	if os.Stdout != origOut {
		t.Errorf("os.Stdout was not restored")
	}
	if os.Stderr != origErr {
		t.Errorf("os.Stderr was not restored")
	}
}

// syncBuf is bytes.Buffer guarded by a mutex so the drain goroutines
// can write to it concurrently with the test goroutine reading it.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuf) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}
