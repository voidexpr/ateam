package agent

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// warnWriter is the sink for agent-package warning lines (malformed
// JSONL, file-creation failures, …). Defaults to os.Stderr so existing
// non-pool callers keep working unchanged.
//
// cmd routes this to the live renderer's interleaving writer for the
// duration of a pool run, so stray warnings appear above the status
// table instead of corrupting its cursor accounting. SetWarnWriter is
// safe to call concurrently with Warnf.
var (
	warnMu sync.RWMutex
	warnW  io.Writer = os.Stderr
)

// SetWarnWriter installs w as the destination for Warnf output. Pass
// nil to restore os.Stderr. Returns the previous writer so callers can
// stack and restore (defer agent.SetWarnWriter(prev)).
func SetWarnWriter(w io.Writer) (prev io.Writer) {
	warnMu.Lock()
	prev = warnW
	if w == nil {
		warnW = os.Stderr
	} else {
		warnW = w
	}
	warnMu.Unlock()
	return prev
}

// Warnf writes one formatted warning line to the configured writer.
// Use this instead of fmt.Fprintf(os.Stderr, …) for warnings emitted
// during agent execution — direct stderr writes corrupt the live
// renderer's cursor tracking.
func Warnf(format string, args ...any) {
	warnMu.RLock()
	w := warnW
	warnMu.RUnlock()
	fmt.Fprintf(w, format, args...)
}
