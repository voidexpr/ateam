package flow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ateam/internal/runner"
)

// BundleLogReporter writes a per-exec_id `bundle.jsonl` chronicling the
// flow framework's lifecycle events for a PromptBundle: bundle_start /
// pre_exec_* / agent_exec_* / post_exec_* / bundle_end. Counterpart to
// the runner's `agent.jsonl` (which carries per-agent progress).
//
// Schema vocabulary is defined in bundle_events.go and shared with
// JSONReporter so the wire format can't drift between the post-mortem
// (this) and live-stream (JSONReporter) surfaces.
//
// One intentional redundancy with agent.jsonl: agent_exec_end carries
// the wrap-totals (cost_usd, input_tokens, output_tokens) so consumers
// can read bundle.jsonl alone for a per-bundle summary.
//
// Lifecycle:
//   - BundleStart buffers a bundle_start event in memory (no exec_id yet).
//   - AgentExecStart opens `<prepared.LogsDir>/bundle.jsonl`, flushes the
//     buffer, then writes synchronously per event.
//   - BundleEnd writes bundle_end and closes the file. If Prepare never
//     fired (Pre returned Skip/Error, Render failed), the buffer is
//     dropped — there is no exec_id directory to write into.
//   - On BundleEnd after a successful run, BundleLogReporter ALSO appends
//     a `## Bundle` section to `<LogsDir>/cmd.md` (the runner finalized
//     cmd.md by the time ExecutePrepared returned, so the append is safe).
//
// Concurrency: callbacks may fire from N Parallel worker goroutines.
// Per-bundle state is keyed by BundleInfo.Name — callers must ensure
// bundle names are unique within a Run. One mutex serializes ALL state
// access (map lookups + file writes); event marshaling happens outside
// the lock so multiple bundles can serialize their JSON in parallel.
//
// Failure mode: disk write errors are logged to stderr and never block
// the run. A partially-written bundle.jsonl is preferable to a hung
// agent.
type BundleLogReporter struct {
	BaseReporter

	mu      sync.Mutex
	bundles map[string]*bundleLogState
}

// bundleLogState holds per-bundle buffer + file handle. Pre-AgentExecStart,
// events accumulate in `buffer` because no exec_id (and thus no LogsDir)
// is known yet. All fields are accessed only with BundleLogReporter.mu held.
type bundleLogState struct {
	startedAt time.Time
	buffer    [][]byte
	file      *os.File
	prepared  *runner.PreparedRun
}

// nowMillis returns the current wall time in unix milliseconds.
var nowMillis = func() int64 { return time.Now().UnixMilli() }

// marshalEvent renders one bundle.jsonl line. Returns the line with a
// trailing newline, ready to write. Safe to call without holding the
// reporter's mutex.
func marshalEvent(kind string, payload map[string]any) []byte {
	m := map[string]any{
		"ts":     nowMillis(),
		"source": "bundle",
		"kind":   kind,
	}
	for k, v := range payload {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		// json.Marshal of map[string]any with primitive values cannot
		// realistically fail; surface anyway in case a caller passes
		// something exotic.
		return []byte(fmt.Sprintf(`{"ts":%d,"source":"bundle","kind":%q,"marshal_err":%q}`, nowMillis(), kind, err.Error()) + "\n")
	}
	return append(b, '\n')
}

// emit marshals `kind`+`payload` outside the lock, then writes the line
// to the named bundle under r.mu. Single helper used by every public
// callback so the lock-and-write boilerplate doesn't repeat.
func (r *BundleLogReporter) emit(name, kind string, payload map[string]any) {
	line := marshalEvent(kind, payload)
	r.mu.Lock()
	r.write(name, line)
	r.mu.Unlock()
}

// write appends a pre-marshaled JSONL line for the named bundle,
// buffering until AgentExecStart opens the file. Caller holds r.mu.
func (r *BundleLogReporter) write(name string, line []byte) {
	st := r.bundles[name]
	if st == nil {
		return
	}
	if st.file == nil {
		st.buffer = append(st.buffer, line)
		return
	}
	if _, err := st.file.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle.jsonl write: %v\n", err)
	}
}

func (r *BundleLogReporter) BundleStart(b BundleInfo) {
	r.mu.Lock()
	if r.bundles == nil {
		r.bundles = map[string]*bundleLogState{}
	}
	r.bundles[b.Name] = &bundleLogState{startedAt: time.Now()}
	r.mu.Unlock()
	r.emit(b.Name, "bundle_start", bundleStartPayload(b))
}

func (r *BundleLogReporter) ActionStart(b BundleInfo, phase ActionPhase, actionType string, index int) {
	r.emit(b.Name, phase.String()+"_start", actionStartPayload(actionType, index))
}

func (r *BundleLogReporter) ActionEnd(b BundleInfo, phase ActionPhase, actionType string, index int, fl Flow, duration time.Duration) {
	r.emit(b.Name, phase.String()+"_end", actionEndPayload(actionType, index, fl, duration))
}

func (r *BundleLogReporter) AgentExecStart(b BundleInfo, prepared *runner.PreparedRun) {
	if prepared == nil {
		return
	}

	// Open <prepared.LogsDir>/bundle.jsonl outside the mutex so parallel
	// bundles don't queue their opens through a single critical section.
	// MkdirAll is idempotent with the runner's own MkdirAll that runs
	// later inside ExecutePrepared.
	if err := os.MkdirAll(prepared.LogsDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle.jsonl mkdir %s: %v\n", prepared.LogsDir, err)
		return
	}
	path := filepath.Join(prepared.LogsDir, runner.BundleFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle.jsonl open %s: %v\n", path, err)
		return
	}
	line := marshalEvent("agent_exec_start", agentExecStartPayload(prepared))

	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.bundles[b.Name]
	if st == nil {
		f.Close()
		return
	}
	st.prepared = prepared
	st.file = f
	for _, buffered := range st.buffer {
		_, _ = f.Write(buffered)
	}
	st.buffer = nil
	r.write(b.Name, line)
}

func (r *BundleLogReporter) AgentExecEnd(b BundleInfo, summary runner.RunSummary) {
	r.emit(b.Name, "agent_exec_end", agentExecEndPayload(summary))
}

func (r *BundleLogReporter) BundleEnd(b BundleInfo, res Result) {
	r.mu.Lock()
	st, ok := r.bundles[b.Name]
	if !ok {
		r.mu.Unlock()
		return
	}
	delete(r.bundles, b.Name)
	duration := time.Since(st.startedAt)
	prepared := st.prepared
	endLine := marshalEvent("bundle_end", bundleEndPayload(res, duration))
	if st.file != nil {
		_, _ = st.file.Write(endLine)
		_ = st.file.Close()
	}
	r.mu.Unlock()

	// cmd.md append happens outside the mutex — it's pure disk I/O and
	// only touches this bundle's own cmd.md. The runner has already
	// re-rendered cmd.md by the time ExecutePrepared returned (and thus
	// AgentExecEnd has fired before this BundleEnd), so the O_APPEND
	// write here will not be clobbered.
	if prepared != nil && res.Summary != nil {
		appendBundleSectionToCmdMD(prepared, b, res)
	}
}

// appendBundleSectionToCmdMD adds a `## Bundle` section at the end of
// cmd.md with the bundle metadata. The runner has already finalized
// cmd.md (Run details, Files Copy, etc.); this section is the flow
// framework's annotation about *why* the run existed.
func appendBundleSectionToCmdMD(prepared *runner.PreparedRun, b BundleInfo, res Result) {
	if prepared.CmdFile == "" {
		return
	}
	var section string
	section += "\n## Bundle\n"
	section += fmt.Sprintf("* name: %s\n", b.Name)
	if b.Role != "" {
		section += fmt.Sprintf("* role: %s\n", b.Role)
	}
	if b.Action != "" {
		section += fmt.Sprintf("* action: %s\n", b.Action)
	}
	if prepared.Opts.WorkDir != "" {
		section += fmt.Sprintf("* work_dir: %s\n", prepared.Opts.WorkDir)
	}
	if prepared.Opts.Batch != "" {
		section += fmt.Sprintf("* batch: %s\n", prepared.Opts.Batch)
	}
	section += fmt.Sprintf("* state: %s\n", res.Flow.State.String())

	f, err := os.OpenFile(prepared.CmdFile, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle cmd.md append %s: %v\n", prepared.CmdFile, err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(section); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle cmd.md write: %v\n", err)
	}
}
