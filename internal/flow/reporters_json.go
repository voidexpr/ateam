package flow

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/ateam/internal/runner"
)

// JSONReporter writes the live event stream of a flow run as JSONL to
// an arbitrary writer — typically a file descriptor passed to the
// orchestrator subprocess via Popen's `pass_fds`. Each line is one
// event; bundle and agent events are interleaved with a `source`
// discriminator (`"bundle"` or `"agent"`).
//
// Bundle events match BundleLogReporter's vocabulary one-for-one (same
// kinds, same payload shapes). Agent events wrap a runner.RunProgress
// — wire format:
//
//	{"ts":<ms>,"source":"agent","exec_id":<id>,"phase":"tool",
//	 "tool_name":"Read","content":"...", ...}
//
// Concurrency: AgentEvent fires from runner goroutines; bundle
// callbacks from PromptBundle.execute. A single mutex serializes all
// line writes so events never interleave mid-line. Writes are
// synchronous — orchestrators flush by reading the fd.
//
// Failure: write errors on the destination fd are silently dropped.
// The orchestrator is responsible for keeping its reader alive; a
// dead reader (e.g. EPIPE) should not crash the agent run.
type JSONReporter struct {
	BaseReporter

	W io.Writer

	mu sync.Mutex
}

// writeEvent marshals m + envelope keys (ts) outside the lock, then
// serializes the line write under r.mu.
func (r *JSONReporter) writeEvent(m map[string]any) {
	m["ts"] = nowMillis()
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, _ = r.W.Write(append(b, '\n'))
}

// bundleEvent wraps a shared kind+payload pair with the source:"bundle"
// envelope and the per-line `name` discriminator, so the orchestrator
// can group events by bundle without parsing each kind separately.
func (r *JSONReporter) bundleEvent(name, kind string, payload map[string]any) {
	m := map[string]any{
		"source": "bundle",
		"kind":   kind,
		"name":   name,
	}
	for k, v := range payload {
		m[k] = v
	}
	r.writeEvent(m)
}

func (r *JSONReporter) BundleStart(b BundleInfo) {
	r.bundleEvent(b.Name, "bundle_start", bundleStartPayload(b))
}

func (r *JSONReporter) BundleEnd(b BundleInfo, res Result) {
	// duration_ms is intentionally omitted on the live stream — the
	// orchestrator computes wall-time from ts(bundle_end)-ts(bundle_start)
	// itself. BundleLogReporter includes it for post-mortem readers.
	r.bundleEvent(b.Name, "bundle_end", map[string]any{
		"state":  res.Flow.State.String(),
		"reason": res.Flow.Reason,
	})
}

func (r *JSONReporter) ActionStart(b BundleInfo, phase ActionPhase, actionType string, index int) {
	r.bundleEvent(b.Name, phase.String()+"_start", actionStartPayload(actionType, index))
}

func (r *JSONReporter) ActionEnd(b BundleInfo, phase ActionPhase, actionType string, index int, fl Flow, duration time.Duration) {
	r.bundleEvent(b.Name, phase.String()+"_end", actionEndPayload(actionType, index, fl, duration))
}

func (r *JSONReporter) AgentExecStart(b BundleInfo, prepared *runner.PreparedRun) {
	if prepared == nil {
		return
	}
	r.bundleEvent(b.Name, "agent_exec_start", agentExecStartPayload(prepared))
}

func (r *JSONReporter) AgentExecEnd(b BundleInfo, summary runner.RunSummary) {
	r.bundleEvent(b.Name, "agent_exec_end", agentExecEndPayload(summary))
}

func (r *JSONReporter) AgentEvent(b BundleInfo, p runner.RunProgress) {
	m := map[string]any{
		"source":  "agent",
		"exec_id": p.ExecID,
		"name":    b.Name,
		"phase":   p.Phase,
	}
	if p.Subtype != "" {
		m["subtype"] = p.Subtype
	}
	if p.ToolName != "" {
		m["tool_name"] = p.ToolName
	}
	if p.ToolInput != "" {
		m["tool_input"] = p.ToolInput
	}
	if p.Content != "" {
		m["content"] = p.Content
	}
	if p.ToolCount > 0 {
		m["tool_count"] = p.ToolCount
	}
	if p.TurnCount > 0 {
		m["turn_count"] = p.TurnCount
	}
	if p.EventCount > 0 {
		m["event_count"] = p.EventCount
	}
	if p.Model != "" {
		m["model"] = p.Model
	}
	if p.SessionID != "" {
		m["session_id"] = p.SessionID
	}
	if p.ContextTokens > 0 {
		m["context_tokens"] = p.ContextTokens
	}
	if p.ContextWindow > 0 {
		m["context_window"] = p.ContextWindow
	}
	if p.CumulativeInputTokens > 0 {
		m["cum_input_tokens"] = p.CumulativeInputTokens
	}
	if p.CumulativeOutputTokens > 0 {
		m["cum_output_tokens"] = p.CumulativeOutputTokens
	}
	if p.EstimatedCost > 0 {
		m["est_cost"] = p.EstimatedCost
	}
	if p.Elapsed > 0 {
		m["elapsed_ms"] = p.Elapsed.Milliseconds()
	}
	r.writeEvent(m)
}
