package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

func addProfileFlags(cmd *cobra.Command, profileDst, agentDst *string) {
	cmd.Flags().StringVar(profileDst, "profile", "", "runtime profile (overrides config resolution)")
	cmd.Flags().StringVar(agentDst, "agent", "", "agent name from runtime.hcl (shortcut, uses 'none' container)")
	cmd.MarkFlagsMutuallyExclusive("profile", "agent")
}

func addContainerNameFlag(cmd *cobra.Command, dst *string) {
	cmd.Flags().StringVar(dst, "container-name", "", "override container name (for docker-exec or persistent containers)")
}

// addBudgetFlags registers --max-budget-usd and --max-budget-usd-batch on cmd.
// perAgentDesc and batchDesc are the help strings, which vary per command
// (e.g. "per-agent" vs "per-role" vs "for the supervisor and every sub-run").
// Pass batchDst = nil for single-exec commands that don't manage a batch.
func addBudgetFlags(cmd *cobra.Command, perAgentDst, batchDst *string, perAgentDesc, batchDesc string) {
	cmd.Flags().StringVar(perAgentDst, "max-budget-usd", "", perAgentDesc)
	if batchDst != nil {
		cmd.Flags().StringVar(batchDst, "max-budget-usd-batch", "", batchDesc)
	}
}

const cheaperModelName = "sonnet"

func addVerboseFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "verbose", false, "print agent and docker commands to stderr")
}

func addCheaperModelFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "cheaper-model", false,
		"use a cheaper model ("+cheaperModelName+"); ignored if --model is also set (--model wins)")
}

// applyEffort sets the agent's reasoning effort if value is non-empty.
// Empty string is a no-op so callers can pass through unconditionally.
// The value is also stashed on the executor so BuildTemplateVars can
// surface it as {{EFFORT}} for supervisor prompts.
func applyEffort(r *runner.AgentExecutor, value string) {
	if value != "" {
		r.Agent.SetEffort(value)
		r.Effort = value
	}
}

// applyModel sets the agent's model override if value is non-empty.
// Empty string is a no-op so callers can pass through unconditionally.
func applyModel(r *runner.AgentExecutor, value string) {
	if value != "" {
		r.Agent.SetModel(value)
	}
}

// parseBudgetUSD parses a USD amount from a CLI flag string. Empty returns 0/false.
func parseBudgetUSD(value string) (float64, bool, error) {
	if value == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false, fmt.Errorf("invalid USD amount %q: %w", value, err)
	}
	if v < 0 {
		return 0, false, fmt.Errorf("budget cap must be non-negative, got %v", v)
	}
	return v, true, nil
}

// batchBudgetPrecheck builds a closure that returns an error once the recorded
// cost for `batch` crosses `capStr`. Returns (nil, nil) when no cap was
// requested. Used both as a one-shot check (for single-exec actions) and as a
// PoolOpts.PreDispatch hook. The closure only sees committed rows, so up to
// maxParallel in-flight execs can still push the total over the cap; this
// matches the user's "ballpark" tolerance.
func batchBudgetPrecheck(db *calldb.CallDB, projectID, batch, capStr string) (func() error, error) {
	cap, capSet, err := parseBudgetUSD(capStr)
	if err != nil {
		return nil, err
	}
	if !capSet || batch == "" {
		return nil, nil
	}
	return func() error {
		spent, err := db.BatchCostUSD(projectID, batch)
		if err != nil {
			return fmt.Errorf("cannot read batch cost: %w", err)
		}
		if spent >= cap {
			return fmt.Errorf("batch %q reached cap (spent $%.4f / $%.4f)", batch, spent, cap)
		}
		return nil
	}, nil
}

// applyMaxBudgetUSD sets the agent's per-run USD spend cap, gating on whether
// the agent supports it natively. Returns an error when the cap was requested
// but cannot be enforced for an action that requires hard enforcement.
//
// Only Claude has a native --max-budget-usd flag. For Codex / codex-tmux:
//   - parallel/report: warn (best-effort; the run-level batch cap still applies)
//   - exec/review/code/verify: error (single-exec actions don't have a batch
//     fallback, so silently dropping the cap would surprise the operator)
func applyMaxBudgetUSD(r *runner.AgentExecutor, value, action string) error {
	if value == "" {
		return nil
	}
	r.Agent.SetMaxBudgetUSD(value)
	r.MaxBudgetUSD = value
	if r.Agent.Name() != agent.NameCodex && r.Agent.Name() != agent.NameCodexTmux {
		return nil
	}
	switch action {
	case runner.ActionParallel, runner.ActionReport:
		fmt.Fprintf(os.Stderr,
			"Warning: --max-budget-usd is not enforced per-agent on %s; relying on --max-budget-usd-batch (if set) for %s.\n",
			r.Agent.Name(),
			action)
		return nil
	default:
		return fmt.Errorf("--max-budget-usd is not supported on %s for %s (no native cap; rerun with claude or drop the flag)", r.Agent.Name(), action)
	}
}

// applyModelOverrides resolves --cheaper-model and --model into a single
// agent.SetModel call. --model wins if both are set (with a stderr warning),
// so the agent's configured model and the actual run command always agree.
// Use this in any command that exposes both flags; commands that only expose
// --model can keep using applyModel.
func applyModelOverrides(r *runner.AgentExecutor, cheaper bool, model string) {
	chosen := model
	if cheaper && model != "" {
		fmt.Fprintf(os.Stderr,
			"Warning: both --model %q and --cheaper-model set; --model wins (--cheaper-model ignored).\n",
			model)
	} else if cheaper {
		chosen = cheaperModelName
	}
	if chosen != "" {
		r.Agent.SetModel(chosen)
		r.ModelOverride = chosen
	}
}

func addDockerAutoSetupFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "docker-auto-setup", true, "auto-generate .ateam/Dockerfile when using docker profile")
}

func addForceFlag(cmd *cobra.Command, dst *bool) {
	cmd.Flags().BoolVar(dst, "force", false, "run even if the same action+role is already running")
}
