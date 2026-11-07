package cmd

import (
	"fmt"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
)

// printModelResolution prints which model and effort the runner will use
// and the layer chain that decided it. Used by every --dry-run.
func printModelResolution(r *runner.AgentExecutor, env *root.ResolvedEnv) {
	rtCfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil {
		return
	}
	prof, ac := resolveProfileAgent(rtCfg, r.Profile)
	if ac == nil {
		return
	}
	printModelReport(rtCfg.ModelReport(prof, ac, runtime.CLIOverrides{Model: r.ModelOverride, Effort: r.Effort}))
}

func printModelReport(rep runtime.ModelReport) {
	fmt.Printf("Model:     %s\n", rep.ModelText())
	fmt.Printf("Effort:    %s\n", rep.EffortText())
	fmt.Println("Resolved from (lowest to highest precedence):")
	for _, line := range rep.ChainLines() {
		fmt.Printf("  %s\n", line)
	}
}
