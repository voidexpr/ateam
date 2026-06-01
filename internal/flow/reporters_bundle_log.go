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
// the runner's `stream.jsonl` (which carries per-agent progress).
//
// The schema is v1 and lives in plans/Feature_prompt_report_fs_refactor_phaseH.md.
// One intentional redundancy with stream.jsonl: agent_exec_end carries
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
// bundle names are unique within a Run. Writes to each bundle's file
// are serialized via its per-state mutex; the top-level map mutex
// covers map mutation only.
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
// is known yet.
type bundleLogState struct {
	mu        sync.Mutex
	info      BundleInfo
	startedAt time.Time
	buffer    [][]byte
	file      *os.File
	prepared  *runner.PreparedRun
	closed    bool
}

// nowMillis returns the current wall time in unix milliseconds.
var nowMillis = func() int64 { return time.Now().UnixMilli() }

// marshalEvent renders one bundle.jsonl line. Returns the line with a
// trailing newline, ready to write.
func marshalEvent(kind string, payload map[string]any) []byte {
	m := map[string]any{
		"v":      1,
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
		return []byte(fmt.Sprintf(`{"v":1,"ts":%d,"source":"bundle","kind":%q,"marshal_err":%q}`, nowMillis(), kind, err.Error()) + "\n")
	}
	return append(b, '\n')
}

// state finds or returns nil for an unknown bundle name. Callers in the
// reporter callbacks treat unknown-name as "we missed BundleStart" and
// skip the event.
func (r *BundleLogReporter) state(name string) *bundleLogState {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bundles == nil {
		return nil
	}
	return r.bundles[name]
}

func (r *BundleLogReporter) BundleStart(b BundleInfo) {
	r.mu.Lock()
	if r.bundles == nil {
		r.bundles = map[string]*bundleLogState{}
	}
	st := &bundleLogState{
		info:      b,
		startedAt: time.Now(),
	}
	r.bundles[b.Name] = st
	r.mu.Unlock()

	st.append(marshalEvent("bundle_start", map[string]any{
		"name":     b.Name,
		"role":     b.Role,
		"action":   b.Action,
		"work_dir": b.WorkDir,
		"batch":    b.Batch,
	}))
}

func (r *BundleLogReporter) ActionStart(b BundleInfo, phase ActionPhase, actionType string, index int) {
	st := r.state(b.Name)
	if st == nil {
		return
	}
	st.append(marshalEvent(phase.String()+"_start", map[string]any{
		"action_type": actionType,
		"index":       index,
	}))
}

func (r *BundleLogReporter) ActionEnd(b BundleInfo, phase ActionPhase, actionType string, index int, fl Flow, duration time.Duration) {
	st := r.state(b.Name)
	if st == nil {
		return
	}
	st.append(marshalEvent(phase.String()+"_end", map[string]any{
		"action_type": actionType,
		"index":       index,
		"state":       fl.State.String(),
		"reason":      fl.Reason,
		"duration_ms": duration.Milliseconds(),
	}))
}

func (r *BundleLogReporter) AgentExecStart(b BundleInfo, prepared *runner.PreparedRun) {
	st := r.state(b.Name)
	if st == nil || prepared == nil {
		return
	}

	st.mu.Lock()
	st.prepared = prepared
	st.mu.Unlock()

	// Open <prepared.LogsDir>/bundle.jsonl now that we have the exec_id
	// path. MkdirAll is idempotent with the runner's own MkdirAll that
	// happens later inside ExecutePrepared.
	if err := os.MkdirAll(prepared.LogsDir, 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle.jsonl mkdir %s: %v\n", prepared.LogsDir, err)
		return
	}
	path := filepath.Join(prepared.LogsDir, "bundle.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: bundle.jsonl open %s: %v\n", path, err)
		return
	}

	st.mu.Lock()
	st.file = f
	buffered := st.buffer
	st.buffer = nil
	st.mu.Unlock()

	for _, line := range buffered {
		_, _ = f.Write(line)
	}

	st.append(marshalEvent("agent_exec_start", map[string]any{
		"exec_id":      prepared.ExecID,
		"model":        prepared.Model,
		"prompt_bytes": prepared.PromptBytes,
	}))
}

func (r *BundleLogReporter) AgentExecEnd(b BundleInfo, summary runner.RunSummary) {
	st := r.state(b.Name)
	if st == nil {
		return
	}
	st.append(marshalEvent("agent_exec_end", map[string]any{
		"exec_id":       summary.ExecID,
		"duration_ms":   summary.Duration.Milliseconds(),
		"is_error":      summary.IsError,
		"exit_code":     summary.ExitCode,
		"cost_usd":      summary.Cost,
		"input_tokens":  summary.InputTokens,
		"output_tokens": summary.OutputTokens,
	}))
}

func (r *BundleLogReporter) BundleEnd(b BundleInfo, res Result) {
	r.mu.Lock()
	st, ok := r.bundles[b.Name]
	if ok {
		delete(r.bundles, b.Name)
	}
	r.mu.Unlock()
	if !ok {
		return
	}

	duration := time.Since(st.startedAt)
	st.append(marshalEvent("bundle_end", map[string]any{
		"state":       res.Flow.State.String(),
		"reason":      res.Flow.Reason,
		"duration_ms": duration.Milliseconds(),
	}))

	st.close()

	// Append the `## Bundle` section to cmd.md when the runner produced
	// an exec_id directory and finished cleanly. The runner has already
	// re-rendered cmd.md by the time ExecutePrepared returned (and thus
	// AgentExecEnd has already fired before this BundleEnd), so an
	// O_APPEND write here will not be clobbered.
	if st.prepared != nil && res.Summary != nil {
		appendBundleSectionToCmdMD(st.prepared, b, res)
	}
}

// append writes one JSONL line to the per-bundle file, buffering if the
// file isn't open yet. Always non-blocking; disk errors go to stderr.
func (st *bundleLogState) append(line []byte) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.closed {
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

func (st *bundleLogState) close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.closed = true
	if st.file != nil {
		_ = st.file.Close()
		st.file = nil
	}
	st.buffer = nil
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
