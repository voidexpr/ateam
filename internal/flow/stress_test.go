package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	runtimepkg "runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ateam/internal/prompts"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/runner"
)

// TestStress_NestedPipelineParallel builds a 100-bundle nested composition
// driven by the REAL runner.AgentExecutor backed by agent.MockAgent. The
// goal is to exercise the new callback-based progress path under heavy
// concurrent fan-in from Parallel.
//
// Shape:
//
//	Pipeline(
//	  Parallel(40 bundles),                   // batch 1 — wide fan-out
//	  Parallel(20 × Pipeline(B,B)),           // batch 2 — Pipeline-in-Parallel (40 bundles)
//	  Parallel(20 bundles),                   // batch 3 — wide fan-out
//	)
//
// 100 bundles total. With Workers=16 on each Parallel, the semaphore is
// exercised. Inner Pipelines test that nested-Pipeline flattening works
// correctly (each inner pipeline must run B then B sequentially).
//
// Reporter counts every callback. Race detector catches goroutine-internal
// data races. Assertions:
//   - exactly 100 BundleStart + 100 BundleEnd
//   - StageStart count == StageEnd count (balanced)
//   - PipelineResult: 3 step outcomes, FirstErrorIndex == -1
//   - every result has StateContinue
//   - no leak: every Bundle.RunOpts seen exactly once on the executor
//
// Run under -race for full effect.
func TestStress_NestedPipelineParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test skipped in -short mode")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("calldb.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Real runner + mock agent. Small Delay introduces real concurrency so
	// workers actually overlap; the mock's stream events drive the
	// callback path through runner.AgentExecutor.Execute → onProgress.
	mock := &agent.MockAgent{
		Response: "stress-ok",
		Cost:     0.001,
		Delay:    2 * time.Millisecond,
	}
	exec := &runner.AgentExecutor{
		Agent:      mock,
		ProjectDir: dir,
		CallDB:     db,
	}

	rep := newCountingReporter()
	bundleLog := &BundleLogReporter{}
	rc := RunCtx{Ctx: context.Background(), DB: db, Reporter: MultiReporter{rep, bundleLog}}
	env := RuntimeEnv{Executor: exec, WorkDir: dir, Role: "stress", Action: "exec"}

	composition := buildStressComposition()

	deadline := time.AfterFunc(20*time.Second, func() {
		// Dump every goroutine's stack so deadlock state is diagnosable.
		buf := make([]byte, 1<<20)
		n := runtimepkg.Stack(buf, true)
		panic(fmt.Sprintf("stress test deadlocked at 20s\n\n=== goroutine dump (%d bytes) ===\n%s", n, buf[:n]))
	})
	defer deadline.Stop()

	result := Run(composition, env, rc)

	// ── structural assertions ────────────────────────────────────────
	if got, want := len(result.Steps), 3; got != want {
		t.Errorf("top-level Pipeline step count: got %d want %d", got, want)
	}
	if result.FirstErrorIndex != -1 {
		t.Errorf("FirstErrorIndex: got %d want -1", result.FirstErrorIndex)
	}

	// ── leaf counts ──────────────────────────────────────────────────
	if got, want := rep.bundleStarts.Load(), int64(100); got != want {
		t.Errorf("BundleStart count: got %d want %d", got, want)
	}
	if got, want := rep.bundleEnds.Load(), int64(100); got != want {
		t.Errorf("BundleEnd count: got %d want %d", got, want)
	}

	// ── stage balance ────────────────────────────────────────────────
	if rep.stageStarts.Load() != rep.stageEnds.Load() {
		t.Errorf("StageStart/End imbalance: %d / %d", rep.stageStarts.Load(), rep.stageEnds.Load())
	}

	// ── every bundle saw exactly one Start and one End ──────────────
	rep.mu.Lock()
	for name, count := range rep.starts {
		if count != 1 {
			t.Errorf("bundle %q: BundleStart count %d (want 1)", name, count)
		}
	}
	for name, count := range rep.ends {
		if count != 1 {
			t.Errorf("bundle %q: BundleEnd count %d (want 1)", name, count)
		}
	}
	if got, want := len(rep.starts), 100; got != want {
		t.Errorf("unique bundle names started: got %d want %d", got, want)
	}
	if got, want := len(rep.ends), 100; got != want {
		t.Errorf("unique bundle names ended: got %d want %d", got, want)
	}
	rep.mu.Unlock()

	// ── per-bundle terminal state ────────────────────────────────────
	totalLeaves := 0
	for _, step := range result.Steps {
		for _, r := range step.Results {
			totalLeaves++
			if r.Flow.State != StateContinue {
				t.Errorf("leaf %q: state %v (want continue) — reason=%q",
					r.Bundle.Name, r.Flow.State, r.Flow.Reason)
			}
			if r.Summary == nil {
				t.Errorf("leaf %q: nil Summary on successful run", r.Bundle.Name)
			}
		}
	}
	if totalLeaves != 100 {
		t.Errorf("total leaf results: got %d want 100", totalLeaves)
	}

	// ── agent invocations match leaf count ───────────────────────────
	if got := len(mock.Requests); got != 100 {
		t.Errorf("MockAgent saw %d requests; want 100", got)
	}

	// ── reasonable progress event volume from real runner ───────────
	// MockAgent emits system + assistant + result + done events per run;
	// the runner translates each into RunProgress. With 100 runs we
	// expect at least 100 events. (Exact count varies by runner internals
	// — we just want "non-zero, no obvious drops".)
	if got := rep.agentEvents.Load(); got < int64(100) {
		t.Errorf("AgentEvent count too low: got %d want ≥ 100", got)
	}
	t.Logf("agent events forwarded: %d", rep.agentEvents.Load())

	// ── bundle.jsonl produced for every leaf ─────────────────────────
	// The MultiReporter included a BundleLogReporter; every bundle that
	// reached AgentExecStart should have a logs/<exec_id>/bundle.jsonl
	// with at least bundle_start + agent_exec_start + agent_exec_end +
	// bundle_end. Race conditions in the reporter would show up as
	// missing files or truncated event sequences.
	logsRoot := filepath.Join(dir, "logs")
	entries, err := os.ReadDir(logsRoot)
	if err != nil {
		t.Fatalf("read logs dir: %v", err)
	}
	if len(entries) != 100 {
		t.Errorf("logs/ entries: got %d want 100", len(entries))
	}
	wantKinds := map[string]bool{
		"bundle_start":     true,
		"agent_exec_start": true,
		"agent_exec_end":   true,
		"bundle_end":       true,
	}
	for _, e := range entries {
		bundlePath := filepath.Join(logsRoot, e.Name(), "bundle.jsonl")
		data, err := os.ReadFile(bundlePath)
		if err != nil {
			t.Errorf("missing bundle.jsonl for %s: %v", e.Name(), err)
			continue
		}
		seen := map[string]bool{}
		for _, line := range bytes.Split(data, []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal(line, &m); err != nil {
				t.Errorf("%s: parse line %q: %v", e.Name(), line, err)
				continue
			}
			if k, _ := m["kind"].(string); k != "" {
				seen[k] = true
			}
		}
		for k := range wantKinds {
			if !seen[k] {
				t.Errorf("%s/bundle.jsonl missing kind %q", e.Name(), k)
			}
		}
	}
}

// ============================================================
// Composition builder
// ============================================================

func buildStressComposition() Step {
	mkBundle := func(name string) PromptBundle {
		return PromptBundle{
			Name:   name,
			Prompt: prompts.RawTextPrompt{Text: "stress: " + name},
			RunOpts: func(env RuntimeEnv) runner.RunOpts {
				return runner.RunOpts{
					RoleID: name,
					Action: runner.ActionExec,
				}
			},
		}
	}

	// Batch 1: wide Parallel of 40 leaves.
	batch1Steps := make([]Step, 40)
	for i := 0; i < 40; i++ {
		batch1Steps[i] = mkBundle(fmt.Sprintf("b1-%02d", i))
	}

	// Batch 2: Parallel of 20 inner Pipelines × 2 leaves each = 40 bundles.
	batch2Steps := make([]Step, 20)
	for i := 0; i < 20; i++ {
		batch2Steps[i] = Pipeline{
			Name: fmt.Sprintf("b2-p%02d", i),
			Steps: []Step{
				mkBundle(fmt.Sprintf("b2-%02d-a", i)),
				mkBundle(fmt.Sprintf("b2-%02d-b", i)),
			},
		}
	}

	// Batch 3: wide Parallel of 20 leaves.
	batch3Steps := make([]Step, 20)
	for i := 0; i < 20; i++ {
		batch3Steps[i] = mkBundle(fmt.Sprintf("b3-%02d", i))
	}

	// Total: 40 + 40 + 20 = 100 bundles.
	return Pipeline{
		Name: "stress",
		Steps: []Step{
			Parallel{Name: "batch1", Steps: batch1Steps, Workers: 16},
			Parallel{Name: "batch2", Steps: batch2Steps, Workers: 16},
			Parallel{Name: "batch3", Steps: batch3Steps, Workers: 16},
		},
	}
}

// ============================================================
// countingReporter
// ============================================================

// countingReporter tracks per-bundle start/end counts and global event
// totals. Safe for concurrent access — every method acquires mu before
// touching the maps; counters use sync/atomic for cheap stats.
type countingReporter struct {
	BaseReporter

	stageStarts  atomic.Int64
	stageEnds    atomic.Int64
	bundleStarts atomic.Int64
	bundleEnds   atomic.Int64
	agentEvents  atomic.Int64
	stepSkipped  atomic.Int64

	mu     sync.Mutex
	starts map[string]int
	ends   map[string]int
}

func newCountingReporter() *countingReporter {
	return &countingReporter{
		starts: map[string]int{},
		ends:   map[string]int{},
	}
}

func (r *countingReporter) StageStart(StageInfo)             { r.stageStarts.Add(1) }
func (r *countingReporter) StageEnd(StageInfo, StageOutcome) { r.stageEnds.Add(1) }
func (r *countingReporter) StepSkipped(StageInfo, string, string) {
	r.stepSkipped.Add(1)
}
func (r *countingReporter) BundleStart(b BundleInfo) {
	r.bundleStarts.Add(1)
	r.mu.Lock()
	r.starts[b.Name]++
	r.mu.Unlock()
}
func (r *countingReporter) BundleEnd(b BundleInfo, _ Result) {
	r.bundleEnds.Add(1)
	r.mu.Lock()
	r.ends[b.Name]++
	r.mu.Unlock()
}
func (r *countingReporter) AgentEvent(BundleInfo, runner.RunProgress) {
	r.agentEvents.Add(1)
}
