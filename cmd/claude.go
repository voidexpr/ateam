package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	claudeConfigDir string
	claudeRaw       bool
	claudeDryRun    bool
)

var claudeCmd = &cobra.Command{
	Use:   "claude [flags] [-- CLAUDE_ARGS...]",
	Short: "Run claude with a shared config directory",
	Long: `Run interactive claude using CLAUDE_CONFIG_DIR so all state (.credentials.json,
.claude.json, settings.json) is stored in a single directory.

By default adds --dangerously-skip-permissions and --remote-control.
Use --raw to run claude without these flags.

This is the recommended way to run interactive claude in Docker containers
with a shared config mount. CLAUDE_CONFIG_DIR is set only for the claude
process — it does not affect ateam agent execution.

Examples:
  ateam claude                                      # uses <orgDir>/claude_linux_shared
  ateam claude --config-dir ~/shared_claude          # explicit path
  ateam claude --raw                                 # no default flags
  ateam claude -- -p "hello"                         # pass args to claude`,
	Args: cobra.ArbitraryArgs,
	RunE: runClaude,
}

func init() {
	claudeCmd.Flags().StringVar(&claudeConfigDir, "config-dir", "", "shared config directory (default: <orgDir>/claude_linux_shared)")
	claudeCmd.Flags().BoolVar(&claudeRaw, "raw", false, "run claude without --dangerously-skip-permissions and --remote-control")
	claudeCmd.Flags().BoolVar(&claudeDryRun, "dry-run", false, "show what would be executed without running")
}

func runClaude(cmd *cobra.Command, args []string) error {
	if goruntime.GOOS != "linux" {
		return fmt.Errorf("ateam claude is only supported on Linux (current: %s)", goruntime.GOOS)
	}
	if !runner.IsInContainer() {
		return fmt.Errorf("ateam claude must be run inside a Docker container (no /.dockerenv found)")
	}

	configDir := claudeConfigDir

	if configDir == "" {
		env, _ := root.Lookup("", "")
		if env != nil && env.OrgDir != "" {
			configDir = filepath.Join(env.OrgDir, defaultSharedClaudePath)
		}
	}

	if configDir == "" {
		return fmt.Errorf("no --config-dir specified and no .ateamorg found for default")
	}

	info, err := os.Stat(configDir)
	if err != nil {
		return fmt.Errorf("config directory %s does not exist", configDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", configDir)
	}

	binary, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	var claudeArgs []string
	if !claudeRaw {
		claudeArgs = append(claudeArgs, "--dangerously-skip-permissions", "--remote-control")
	}
	claudeArgs = append(claudeArgs, args...)

	env := os.Environ()
	env = setEnv(env, "CLAUDE_CONFIG_DIR", configDir)

	var unsetKeys []string
	for _, key := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if os.Getenv(key) != "" {
			unsetKeys = append(unsetKeys, key)
		}
		env = unsetEnv(env, key)
	}

	prefix := ""
	if claudeDryRun {
		prefix = "[dry-run] "
	}

	fmt.Printf("  %sset   CLAUDE_CONFIG_DIR=%s\n", prefix, configDir)
	for _, key := range unsetKeys {
		fmt.Printf("  %sunset %s\n", prefix, key)
	}
	fmt.Printf("  %sexec  %s %s\n\n", prefix, binary, strings.Join(claudeArgs, " "))

	if claudeDryRun {
		return nil
	}

	argv := append([]string{"claude"}, claudeArgs...)
	return syscall.Exec(binary, argv, env)
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
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
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
			continue
		}
		out = append(out, e)
	}
	return out
}
