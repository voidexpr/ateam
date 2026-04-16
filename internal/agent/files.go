package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
)

// setupStreamFiles creates the stderr and stream file writers for an agent run.
// The caller is responsible for closing all returned closers (typically via defer).
// The stream file closer also flushes the bufio.Writer before closing.
func setupStreamFiles(req Request) (stderrWriters []io.Writer, streamWriter *bufio.Writer, closers []io.Closer) {
	var stderrBuf bytes.Buffer
	stderrWriters = []io.Writer{&stderrBuf}
	if req.StderrFile != "" {
		if ef, err := os.Create(req.StderrFile); err == nil {
			closers = append(closers, ef)
			stderrWriters = append(stderrWriters, ef)
		} else {
			fmt.Fprintf(os.Stderr, "warning: cannot create stderr file %s: %v\n", req.StderrFile, err)
		}
	}
	if req.StreamFile != "" {
		if sf, err := os.Create(req.StreamFile); err == nil {
			w := bufio.NewWriter(sf)
			streamWriter = w
			closers = append(closers, &flushCloser{w: w, c: sf})
		} else {
			fmt.Fprintf(os.Stderr, "warning: cannot create stream file %s: %v\n", req.StreamFile, err)
		}
	}
	return
}

// flushCloser wraps a bufio.Writer and an io.Closer, flushing before closing.
type flushCloser struct {
	w *bufio.Writer
	c io.Closer
}

func (fc *flushCloser) Close() error {
	fc.w.Flush()
	return fc.c.Close()
}
