package cmd

import (
	"fmt"
	"os"
	goruntime "runtime"
	"strings"
	"syscall"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
	"github.com/spf13/cobra"
)

var (
	claudeConfigDir string
	claudeProfile   string
	claudeAgent     string
	claudeRaw       bool
	claudeDryRun    bool
)

var claudeCmd = &cobra.Command{
	Use:   "claude [flags] [-- CLAUDE_ARGS...]",
	Short: "Run interactive claude inside a container with the unattended agent's config dir",
	Long: `Run interactive claude with CLAUDE_CONFIG_DIR pointing at the config
directory the unattended agent uses (the agent's config_dir in runtime.hcl,
picked like 'ateam login': --agent, else --profile, else the default
profile), so one login serves both. --config-dir overrides the resolved
path; with no config_dir on the agent and no flag, claude uses its own
default directory.

By default adds --dangerously-skip-permissions and --remote-control.
Use --raw to run claude without these flags. Only runs on Linux inside
a Docker container; on the host use 'ateam login' to authenticate.

Examples:
  ateam claude                                       # default profile's agent config dir
  ateam claude --config-dir /path/in/container        # explicit path (e.g. a mounted dir)
  ateam claude --raw                                  # no default flags
  ateam claude -- --name "my-session"                 # pass args to claude
  ateam claude -- -p "hello"                          # headless mode`,
	Args: cobra.ArbitraryArgs,
	RunE: runClaude,
}

func init() {
	claudeCmd.Flags().StringVar(&claudeConfigDir, "config-dir", "", "config directory (default: the resolved agent's config_dir)")
	addProfileFlags(claudeCmd, &claudeProfile, &claudeAgent)
	claudeCmd.Flags().BoolVar(&claudeRaw, "raw", false, "run claude without --dangerously-skip-permissions and --remote-control")
	claudeCmd.Flags().BoolVar(&claudeDryRun, "dry-run", false, "show what would be executed without running")
}

func runClaude(cmd *cobra.Command, args []string) error {
	if goruntime.GOOS != "linux" {
		return fmt.Errorf("ateam claude is only supported on Linux (current: %s); use 'ateam login' on the host", goruntime.GOOS)
	}
	if !container.IsInDockerContainer() {
		return fmt.Errorf("ateam claude must be run inside a Docker container " +
			"(no /.dockerenv or /run/.containerenv found, and ATEAM_IN_CONTAINER not set); " +
			"sandbox-layer detection (Seatbelt/bwrap/fence/firejail/...) is intentionally ignored here — set ATEAM_IN_CONTAINER=1 to override if you know what you're doing")
	}

	env, _ := lookupEnvOptional()
	rtCfg, err := loadRuntime(env)
	if err != nil {
		return err
	}
	target, err := resolveLoginTarget(env, rtCfg, claudeProfile, claudeAgent)
	if err != nil {
		return err
	}
	if target.Type != agent.NameClaude {
		return fmt.Errorf("agent %q is of type %q; ateam claude only runs claude agents", target.Agent.Name, target.Type)
	}
	if claudeConfigDir != "" {
		target.ConfigDir = claudeConfigDir
	}
	if target.ConfigDir != "" {
		info, err := os.Stat(target.ConfigDir)
		if err != nil {
			return fmt.Errorf("config directory %s does not exist", target.ConfigDir)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", target.ConfigDir)
		}
	}

	binary, err := agentBinary(target)
	if err != nil {
		return err
	}

	var claudeArgs []string
	if !claudeRaw {
		claudeArgs = append(claudeArgs, "--dangerously-skip-permissions", "--remote-control")
	}
	claudeArgs = append(claudeArgs, args...)
	childEnv, unset := agentCLIEnv(target)

	prefix := ""
	if claudeDryRun {
		prefix = "[dry-run] "
	}
	fmt.Printf("  %sagent %s (%s)\n", prefix, target.Agent.Name, target.Source)
	if target.ConfigDir != "" {
		fmt.Printf("  %sset   CLAUDE_CONFIG_DIR=%s\n", prefix, target.ConfigDir)
	}
	for _, key := range unset {
		fmt.Printf("  %sunset %s\n", prefix, key)
	}
	fmt.Printf("  %sexec  %s %s\n\n", prefix, binary, strings.Join(claudeArgs, " "))
	if claudeDryRun {
		return nil
	}

	argv := append([]string{"claude"}, claudeArgs...)
	return syscall.Exec(binary, argv, childEnv)
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	out := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}
