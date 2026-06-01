package flow

import (
	"time"

	"github.com/ateam/internal/runner"
)

// ============================================================
// Reporter types
// ============================================================

// StageKind classifies a composition stage for Reporter callbacks.
type StageKind int

const (
	StagePipeline StageKind = iota
	StageParallel
)

func (k StageKind) String() string {
	switch k {
	case StagePipeline:
		return "pipeline"
	case StageParallel:
		return "parallel"
	}
	return "unknown"
}

// StageInfo describes a Pipeline or Parallel stage to Reporter callbacks.
type StageInfo struct {
	Kind     StageKind
	Name     string
	Children int
}

// StageOutcome is the aggregate result of a Pipeline or Parallel stage.
// FirstErrorIndex is meaningful only for Pipeline; -1 for Parallel and
// for error-free Pipelines.
type StageOutcome struct {
	Succeeded       int
	Failed          int
	Skipped         int
	FirstErrorIndex int
}

// BundleInfo describes a PromptBundle to Reporter callbacks.
type BundleInfo struct {
	Name    string
	Role    string
	Action  string
	WorkDir string
	Batch   string
}

// ActionPhase distinguishes PreExec from PostExec actions in
// ActionStart / ActionEnd Reporter callbacks.
type ActionPhase int

const (
	PreExec ActionPhase = iota
	PostExec
)

func (p ActionPhase) String() string {
	switch p {
	case PreExec:
		return "pre_exec"
	case PostExec:
		return "post_exec"
	}
	return "unknown"
}

// ============================================================
// Reporter interface
// ============================================================

// Reporter is the single observability surface for flow executions.
// One Reporter instance is shared across an entire Run(); Pipeline and
// Parallel children call into the same Reporter (Option A in the design
// discussion — see plans/Feature_prompt_report_fs_refactor_phaseG.md).
//
// Implementations own their thread-safety: methods may fire concurrently
// from Parallel children.
type Reporter interface {
	// StageStart fires at the top of every Pipeline.execute and
	// Parallel.execute.
	StageStart(StageInfo)

	// StageEnd fires at the bottom of every Pipeline.execute and
	// Parallel.execute, with aggregate counts.
	StageEnd(StageInfo, StageOutcome)

	// StepSkipped fires for each Pipeline step that did NOT execute
	// because an earlier step errored. The step's own BundleStart /
	// StageStart never fires; this is the only signal a Reporter receives
	// about the skipped step.
	StepSkipped(parent StageInfo, stepName, reason string)

	// BundleStart fires at the top of every PromptBundle.execute, BEFORE
	// PreExec actions run.
	BundleStart(BundleInfo)

	// BundleEnd fires at the bottom of every PromptBundle.execute, AFTER
	// PostExec actions run. The Result is fully populated (Summary may be
	// nil when the bundle skipped before Execute).
	BundleEnd(BundleInfo, Result)

	// AgentEvent fires for each runner.RunProgress event emitted by the
	// agent during Execute. Forwarded from the bundle's internal progress
	// channel. May fire from a goroutine distinct from BundleStart/End.
	AgentEvent(BundleInfo, runner.RunProgress)

	// ActionStart fires immediately before a PreExec or PostExec action's
	// Run method is invoked. index is the action's 0-based position in
	// the bundle's PreExec / PostExec slice.
	ActionStart(b BundleInfo, phase ActionPhase, actionType string, index int)

	// ActionEnd fires immediately after a PreExec or PostExec action
	// returns. flow is the action's return; duration is its wall-time.
	// Mirrors ActionStart's arguments.
	ActionEnd(b BundleInfo, phase ActionPhase, actionType string, index int, flow Flow, duration time.Duration)

	// AgentExecStart fires immediately before Executor.ExecutePrepared is
	// called for this bundle. prepared.ExecID is the allocated exec_id;
	// LogsDir / RuntimeDir etc. carry the per-run paths the runner will
	// write into. Fires only when Prepare succeeded — Prepare-failure
	// bundles produce neither AgentExecStart nor AgentExecEnd.
	AgentExecStart(b BundleInfo, prepared *runner.PreparedRun)

	// AgentExecEnd fires immediately after Executor.ExecutePrepared
	// returns. summary is the full run summary including ExecID, totals,
	// and error state. Paired with AgentExecStart.
	AgentExecEnd(b BundleInfo, summary runner.RunSummary)
}

// BaseReporter is the no-op implementation. Embed it to override only
// the callbacks you care about; the rest stay no-ops.
type BaseReporter struct{}

func (BaseReporter) StageStart(StageInfo)                                                {}
func (BaseReporter) StageEnd(StageInfo, StageOutcome)                                    {}
func (BaseReporter) StepSkipped(StageInfo, string, string)                               {}
func (BaseReporter) BundleStart(BundleInfo)                                              {}
func (BaseReporter) BundleEnd(BundleInfo, Result)                                        {}
func (BaseReporter) AgentEvent(BundleInfo, runner.RunProgress)                           {}
func (BaseReporter) ActionStart(BundleInfo, ActionPhase, string, int)                    {}
func (BaseReporter) ActionEnd(BundleInfo, ActionPhase, string, int, Flow, time.Duration) {}
func (BaseReporter) AgentExecStart(BundleInfo, *runner.PreparedRun)                      {}
func (BaseReporter) AgentExecEnd(BundleInfo, runner.RunSummary)                          {}

// NoopReporter is the explicit "discard everything" Reporter. Used as
// the default when RunCtx.Reporter is nil.
type NoopReporter = BaseReporter
