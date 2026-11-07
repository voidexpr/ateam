package cmd

import (
	"fmt"
	"os"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
)

// printModelResolution prints which model and effort the runner will use
// and the layer chain that decided it. Used by every --dry-run.
func printModelResolution(r *runner.AgentExecutor, env *root.ResolvedEnv) {
	rtCfg, err := loadRuntime(env)
	if err != nil {
		return
	}
	prof, ac := resolveProfileAgent(rtCfg, r.Profile)
	if ac == nil || ac.Type == "builtin" {
		return
	}
	cli := runtime.CLIOverrides{Model: r.ModelOverride, Effort: r.Effort}
	printModelReport(rtCfg.ModelReport(prof, ac, cli, nativeConfigFor(env, rtCfg, prof, ac)))
}

func printModelReport(rep runtime.ModelReport) {
	fmt.Printf("Model:     %s\n", rep.ModelText())
	fmt.Printf("Effort:    %s\n", rep.EffortText())
	fmt.Println("Resolved from (lowest to highest precedence):")
	for _, line := range rep.ChainLines() {
		fmt.Printf("  %s\n", line)
	}
}

// nativeConfigFor reads the agent CLI's own configuration for the run the
// profile describes, the way the run would see it: the agent's config_dir
// and env map first, then the process env. Not inspected for container
// profiles (the container's config is not visible from the host) and
// empty for builtin agents.
func nativeConfigFor(env *root.ResolvedEnv, rtCfg *runtime.Config, prof *runtime.ProfileConfig, ac *runtime.AgentConfig) runtime.NativeConfig {
	if ac == nil || ac.Type == "builtin" {
		return runtime.NativeConfig{}
	}
	agentType := agentTypeName(ac)
	if prof != nil {
		if cc, ok := rtCfg.Containers[prof.Container]; ok && cc.Type != "" && cc.Type != "none" {
			return runtime.NativeConfig{Source: agentType + " native config", Note: "not inspected: the run happens in a container"}
		}
	}
	dir, err := agentConfigDirPath(ac, env.StateDir())
	if err != nil {
		dir = ""
	}
	lookup := func(key string) string {
		if v, ok := ac.Env[key]; ok {
			return v
		}
		return os.Getenv(key)
	}
	return agent.ReadNativeConfig(agentType, dir, lookup)
}
