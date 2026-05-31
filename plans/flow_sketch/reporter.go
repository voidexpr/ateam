//go:build sketch

package flow

import (
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/ateam/internal/runner"
)

// ====== Reporter interface ======

type StageKind int

const (
	StagePipeline StageKind = iota
	StageParallel
)

type StageInfo struct {
	Kind     StageKind
	Name     string
	Children int
}

type StageOutcome struct {
	Succeeded       int
	Failed          int
	Skipped         int
	FirstErrorIndex int // Pipeline-only; -1 otherwise
}

type BundleInfo struct {
	Name   string
	Role   string
	Action string
}

// Reporter is the single observability surface. Each composition node calls
// into the same Reporter instance (Option A). Implementations are
// responsible for their own locking — methods fire concurrently from
// Parallel children.
//
// Naming note: ateam.py would call this RuntimeLogger; in Go, Reporter
// reads better at call sites (rc.Reporter.BundleStart(...)).
type Reporter interface {
	StageStart(StageInfo)
	StageEnd(StageInfo, StageOutcome)
	// StepSkipped fires for each Pipeline step that didn't execute because
	// an earlier step in the same Pipeline errored. The step's own
	// StageStart / BundleStart never fires; this is the only signal.
	StepSkipped(parent StageInfo, stepName, reason string)
	BundleStart(BundleInfo)
	BundleEnd(BundleInfo, Result)
	AgentEvent(BundleInfo, runner.RunProgress)
}

// BaseReporter is the no-op implementation. Embed to override only the
// callbacks you care about.
type BaseReporter struct{}

func (BaseReporter) StageStart(StageInfo)                      {}
func (BaseReporter) StageEnd(StageInfo, StageOutcome)          {}
func (BaseReporter) StepSkipped(StageInfo, string, string)     {}
func (BaseReporter) BundleStart(BundleInfo)                    {}
func (BaseReporter) BundleEnd(BundleInfo, Result)              {}
func (BaseReporter) AgentEvent(BundleInfo, runner.RunProgress) {}

type NoopReporter = BaseReporter

// ====== StdoutReporter — default for `exec` and single-bundle cmds ======

// StdoutReporter streams bundle lifecycle and agent output to stdout in the
// same shape today's exec / verify / review / auto_setup print. Single
// bundles only need the BundleStart / BundleEnd / AgentEvent path; Stage
// events are ignored.
type StdoutReporter struct {
	BaseReporter
	Out io.Writer
	mu  sync.Mutex
}

func (r *StdoutReporter) writer() io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return os.Stdout
}

func (r *StdoutReporter) BundleStart(b BundleInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.writer(), "Starting %s (role=%s action=%s)...\n", b.Name, b.Role, b.Action)
}

func (r *StdoutReporter) BundleEnd(b BundleInfo, res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch res.Flow.State {
	case StateSkip:
		fmt.Fprintf(r.writer(), "Skipped %s: %s\n", b.Name, res.Flow.Reason)
	case StateError:
		fmt.Fprintf(r.writer(), "Failed  %s: %s\n", b.Name, res.Flow.Reason)
	default:
		if res.Summary != nil {
			fmt.Fprintf(r.writer(), "Done    %s in %s\n", b.Name, res.Summary.Duration)
		} else {
			fmt.Fprintf(r.writer(), "Done    %s\n", b.Name)
		}
	}
}

func (r *StdoutReporter) AgentEvent(b BundleInfo, p runner.RunProgress) {
	// In stream mode the agent output already reaches the user via the
	// runner's stderr/stdout passthrough; AgentEvent here is for the
	// structured phase/tool/turn-count signal. Keep it terse so it
	// doesn't interleave noisily with the subprocess output.
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.Phase == "" {
		return
	}
	fmt.Fprintf(r.writer(), "  [%s] turn=%d tools=%d ctx=%d/%d\n",
		p.Phase, p.TurnCount, p.ToolCount, p.ContextTokens, p.ContextWindow)
}

// ====== TableReporter — default for `parallel` and `report` ======

// TableReporter renders the live row-per-bundle table that runPool produces
// today. The existing display logic in cmd/runpool_display.go (or wherever
// it currently lives) moves here and is adapted to consume Reporter
// callbacks instead of a chan-driven loop.
//
// Sketch only — fields and method signatures show the wiring; rendering
// is elided.
type TableReporter struct {
	BaseReporter
	Out io.Writer

	mu       sync.Mutex
	rows     map[string]*tableRow // keyed by BundleInfo.Name
	rendered bool                 // first paint vs subsequent repaints
}

type tableRow struct {
	name      string
	role      string
	started   bool
	done      bool
	skipped   bool
	flowState FlowState
	summary   *runner.RunSummary
	lastEvent runner.RunProgress
}

func (r *TableReporter) StageStart(s StageInfo) {
	if s.Kind != StageParallel {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rows == nil {
		r.rows = map[string]*tableRow{}
	}
	r.repaint()
}

func (r *TableReporter) BundleStart(b BundleInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows[b.Name] = &tableRow{name: b.Name, role: b.Role, started: true}
	r.repaint()
}

func (r *TableReporter) AgentEvent(b BundleInfo, p runner.RunProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row, ok := r.rows[b.Name]; ok {
		row.lastEvent = p
	}
	r.repaint()
}

func (r *TableReporter) BundleEnd(b BundleInfo, res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if row, ok := r.rows[b.Name]; ok {
		row.done = true
		row.flowState = res.Flow.State
		row.skipped = res.Flow.State == StateSkip
		row.summary = res.Summary
	}
	r.repaint()
}

func (r *TableReporter) StageEnd(s StageInfo, _ StageOutcome) {
	if s.Kind != StageParallel {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finalize()
}

// repaint redraws the live table to r.Out. finalize freezes the current
// table and resets internal state so a subsequent StageStart can open a
// fresh table (the nested-Parallel case).
func (r *TableReporter) repaint()  {}
func (r *TableReporter) finalize() {}
