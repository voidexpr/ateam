package cmd

import (
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// RunnerOverrides bundles every CLI-flag override that flows uniformly into
// runner setup across commands. Apply with applyRunnerOverrides; commands that
// need access to MaxBudgetUSDBatch (for batch precheck) can read it directly.
type RunnerOverrides struct {
	ContainerName     string
	CheaperModel      bool
	Model             string
	Effort            string
	MaxBudgetUSD      string
	MaxBudgetUSDBatch string
}

// applyRunnerOverrides runs the per-flag apply* helpers in the order previously
// duplicated across cmd/{code,exec,parallel,report,review,verify}.go. action is
// forwarded to applyMaxBudgetUSD for action-specific gating.
func applyRunnerOverrides(r *runner.Runner, env *root.ResolvedEnv, o RunnerOverrides, action string) error {
	if err := applyContainerName(r, env, o.ContainerName); err != nil {
		return err
	}
	applyModelOverrides(r, o.CheaperModel, o.Model)
	applyEffort(r, o.Effort)
	return applyMaxBudgetUSD(r, o.MaxBudgetUSD, action)
}
