package flow

import (
	"time"

	"github.com/ateam/internal/runner"
)

// Bundle event vocabulary — the single source of truth for the payload
// shape of each `kind` BundleLogReporter writes to bundle.jsonl and
// JSONReporter writes to its progress fd. Both reporters call into the
// builders here so the wire schema can't silently drift between the
// post-mortem and live-stream surfaces.
//
// Each builder returns the payload BODY (kind-specific fields). The
// reporter wraps it with the envelope keys (`ts`, `source`, `kind`,
// plus reporter-specific extras like `name` for JSONReporter).

// bundleStartPayload mirrors the bundle_start event vocabulary.
func bundleStartPayload(b BundleInfo) map[string]any {
	return map[string]any{
		"name":     b.Name,
		"role":     b.Role,
		"action":   b.Action,
		"work_dir": b.WorkDir,
		"batch":    b.Batch,
	}
}

func bundleEndPayload(res Result, duration time.Duration) map[string]any {
	return map[string]any{
		"state":       res.Flow.State.String(),
		"reason":      res.Flow.Reason,
		"duration_ms": duration.Milliseconds(),
	}
}

func actionStartPayload(actionType string, index int) map[string]any {
	return map[string]any{
		"action_type": actionType,
		"index":       index,
	}
}

func actionEndPayload(actionType string, index int, fl Flow, duration time.Duration) map[string]any {
	return map[string]any{
		"action_type": actionType,
		"index":       index,
		"state":       fl.State.String(),
		"reason":      fl.Reason,
		"duration_ms": duration.Milliseconds(),
	}
}

func agentExecStartPayload(prepared *runner.PreparedRun) map[string]any {
	return map[string]any{
		"exec_id":      prepared.ExecID,
		"model":        prepared.Model,
		"prompt_bytes": prepared.PromptBytes,
	}
}

func agentExecEndPayload(summary runner.RunSummary) map[string]any {
	return map[string]any{
		"exec_id":       summary.ExecID,
		"duration_ms":   summary.Duration.Milliseconds(),
		"is_error":      summary.IsError,
		"exit_code":     summary.ExitCode,
		"cost_usd":      summary.Cost,
		"input_tokens":  summary.InputTokens,
		"output_tokens": summary.OutputTokens,
	}
}
