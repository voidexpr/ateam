package cmd

import (
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"syscall"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

var (
	agentCfgAudit            bool
	agentCfgSetupInteractive bool
	agentCfgWipe             bool
	agentCfgContainerOnly    bool
	agentCfgMethod           string
	agentCfgExec             bool
	agentCfgDryRun           bool
	agentCfgRefreshToken     string
	agentCfgSaveRefreshToken bool
)

var agentConfigCmd = &cobra.Command{
	Use:   "agent-config [-- AGENT_ARGS...]",
	Short: "[experimental] Configure Claude Code agent authentication",
	Long: `[experimental] Configure Claude Code agent authentication.

Audit auth state, set up interactive sessions, or wipe agent config.

AUDIT (works everywhere, read-only):
  ateam agent-config --audit

SETUP INTERACTIVE (in a container):
  ateam agent-config --setup-interactive
  # Bootstraps credentials from refresh token, then starts claude

WIPE (in a container):
  ateam agent-config --wipe-i-am-sure
  # Removes all auth state, keeps settings.json, plugins, skills

LEGACY FLAGS (still work):
  ateam agent-config --method oauth --exec -- -p "hello"
  ateam agent-config --save-refresh-token`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgentConfig,
}

func init() {
	agentConfigCmd.Flags().BoolVar(&agentCfgAudit, "audit", false, "[experimental] show auth state, tokens, and export instructions")
	agentConfigCmd.Flags().BoolVar(&agentCfgSetupInteractive, "setup-interactive", false, "[experimental] bootstrap interactive session from refresh token")
	agentConfigCmd.Flags().BoolVar(&agentCfgWipe, "wipe-i-am-sure", false, "[experimental] remove all auth state (keeps settings.json, plugins, skills)")
	agentConfigCmd.Flags().BoolVar(&agentCfgContainerOnly, "container-only", true, "only allow destructive operations inside Docker containers")

	// Legacy flags (backward compat with agent-auth)
	agentConfigCmd.Flags().StringVar(&agentCfgMethod, "method", "", "target auth method: oauth, api, or regular")
	agentConfigCmd.Flags().BoolVar(&agentCfgExec, "exec", false, "exec claude after configuring auth (replaces process)")
	agentConfigCmd.Flags().BoolVarP(&agentCfgDryRun, "dry-run", "n", false, "show what would be done without making changes")
	agentConfigCmd.Flags().BoolVar(&agentCfgSaveRefreshToken, "save-refresh-token", false, "extract refresh token from .credentials.json and save to ateam secrets")
	agentConfigCmd.Flags().StringVar(&agentCfgRefreshToken, "refresh-token", "", "provide a refresh token from .credentials.json")
}

func runAgentConfig(cmd *cobra.Command, args []string) error {
	var projectDir, orgDir string
	if env, err := lookupEnvOptional(); err == nil {
		projectDir = env.ProjectDir
		orgDir = env.OrgDir
	}

	// --audit works everywhere, no container-only check
	if agentCfgAudit {
		return runAgentConfigAudit(projectDir, orgDir)
	}

	// --setup-interactive and --wipe-i-am-sure respect container-only
	if (agentCfgSetupInteractive || agentCfgWipe) && agentCfgContainerOnly && !runner.IsInContainer() {
		return fmt.Errorf("this operation is designed for Docker containers (use --container-only=false to override)")
	}

	if agentCfgSetupInteractive {
		return runSetupInteractive(projectDir, orgDir, args)
	}

	if agentCfgWipe {
		return runWipe(projectDir, orgDir)
	}

	// Legacy flow (--method, --save-refresh-token, etc.)
	if agentCfgContainerOnly && !runner.IsInContainer() {
		// Only enforce for destructive legacy flags
		if agentCfgMethod != "" || agentCfgSaveRefreshToken {
			return fmt.Errorf("agent-config is designed for Docker containers (use --container-only=false to override)")
		}
	}

	if agentCfgWipe && goruntime.GOOS != "linux" {
		return fmt.Errorf("--wipe-i-am-sure is only allowed on Linux (current OS: %s)", goruntime.GOOS)
	}

	if agentCfgExec && agentCfgDryRun {
		return fmt.Errorf("--exec and --dry-run are incompatible")
	}

	if agentCfgRefreshToken != "" {
		if err := storeRefreshToken(agentCfgRefreshToken, projectDir, orgDir); err != nil {
			return err
		}
		fmt.Println("Refresh token saved to ateam secrets.")
	}

	status := agent.DetectAuth(projectDir, orgDir)

	if agentCfgSaveRefreshToken {
		return saveRefreshToken(status, projectDir, orgDir)
	}

	if agentCfgMethod == "" {
		return fmt.Errorf("specify --audit, --setup-interactive, --wipe-i-am-sure, or --method")
	}
	target, ok := agent.ParseAuthMethod(agentCfgMethod)
	if !ok {
		return fmt.Errorf("invalid --method %q (use oauth, api, or regular)", agentCfgMethod)
	}

	return runLegacyAuthFlow(target, status, projectDir, orgDir, args)
}

func runAgentConfigAudit(projectDir, orgDir string) error {
	fmt.Println("[experimental] Claude Code Agent Configuration Audit")
	fmt.Println()

	status := agent.DetectAuth(projectDir, orgDir)

	fmt.Printf("Config dir:       %s\n", status.ConfigDir)
	fmt.Printf("Active auth:      %s\n", status.Active)
	fmt.Println()

	printAuthSources(status)

	// If interactive login detected, print export instructions
	refreshToken := agent.ExtractRefreshToken(status.ConfigDir)
	if refreshToken != "" {
		fmt.Println("Interactive session detected. To use in another environment:")
		fmt.Println()
		fmt.Printf("  export CLAUDE_CODE_OAUTH_REFRESH_TOKEN=%s\n", maskToken(refreshToken))
		fmt.Println("  export CLAUDE_CODE_OAUTH_SCOPES=\"user:profile user:inference\"")
		fmt.Println()
		fmt.Println("  Or save to ateam secrets:")
		fmt.Println("    ateam agent-config --save-refresh-token")
		fmt.Println()
	}

	if val := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); val != "" {
		fmt.Println("Headless token detected. To use in another environment:")
		fmt.Println()
		fmt.Printf("  export CLAUDE_CODE_OAUTH_TOKEN=%s\n", maskToken(val))
		fmt.Println()
	} else if status.HasSecretOAuth {
		resolver := secret.NewResolver(projectDir, orgDir, secret.DefaultBackend(), nil)
		if r := resolver.Resolve("CLAUDE_CODE_OAUTH_TOKEN"); r.Found {
			fmt.Println("Headless token detected. To use in another environment:")
			fmt.Println()
			fmt.Printf("  export CLAUDE_CODE_OAUTH_TOKEN=%s\n", maskToken(r.Value))
			fmt.Println()
		}
	}

	return nil
}

func runSetupInteractive(projectDir, orgDir string, args []string) error {
	fmt.Println("[experimental] Setting up interactive Claude Code session...")
	fmt.Println()

	status := agent.DetectAuth(projectDir, orgDir)

	// Clean stale state
	results := agent.Cleanup(status.ConfigDir, false, false)
	results = append(results, agent.EnsureClaudeState(status.ConfigDir, false))
	for _, r := range results {
		if r.Action != "skip" {
			fmt.Printf("  [%s] %s\n", r.Action, r.Description)
		}
	}

	return execClaude(agent.AuthRegular, status, projectDir, orgDir, args)
}

func runWipe(projectDir, orgDir string) error {
	if goruntime.GOOS != "linux" {
		return fmt.Errorf("--wipe-i-am-sure is only allowed on Linux (current OS: %s)", goruntime.GOOS)
	}

	fmt.Println("[experimental] Wiping Claude Code configuration...")
	fmt.Println()

	status := agent.DetectAuth(projectDir, orgDir)
	results := agent.Cleanup(status.ConfigDir, true, false)
	results = append(results, agent.EnsureClaudeState(status.ConfigDir, false))

	for _, r := range results {
		if r.Action != "skip" {
			fmt.Printf("  [%s] %s\n", r.Action, r.Description)
		}
	}

	fmt.Println()
	fmt.Println("Config wiped. Only settings.json preserved.")
	return nil
}

func runLegacyAuthFlow(target agent.AuthMethod, status agent.AuthStatus, projectDir, orgDir string, args []string) error {
	fmt.Println("Claude Code Auth")
	fmt.Printf("  Config dir:       %s\n", status.ConfigDir)
	fmt.Printf("  Active method:    %s\n", status.Active)
	fmt.Printf("  Target method:    %s\n", target)
	fmt.Println()

	printAuthSources(status)

	if status.Active != target {
		if msg := agent.ValidateTarget(target, status); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		if warnings := agent.Conflicts(target); len(warnings) > 0 {
			for _, w := range warnings {
				fmt.Printf("  [warn] %s\n", w)
			}
			fmt.Println()
		}
	}

	results := agent.Cleanup(status.ConfigDir, false, agentCfgDryRun)
	results = append(results, agent.EnsureClaudeState(status.ConfigDir, agentCfgDryRun))

	hasActions := false
	for _, r := range results {
		if r.Action != "skip" {
			hasActions = true
			break
		}
	}

	if hasActions {
		if agentCfgDryRun {
			fmt.Println("Actions (dry-run):")
		} else {
			fmt.Println("Actions:")
		}
		for _, r := range results {
			if r.Action == "skip" {
				continue
			}
			fmt.Printf("  [%s] %s\n", r.Action, r.Description)
		}
		fmt.Println()
	}

	if agentCfgDryRun {
		if hasActions {
			fmt.Println("Dry-run complete, no changes made.")
		} else {
			fmt.Println("Already configured, no changes needed.")
		}
		return nil
	}

	if hasActions {
		fmt.Printf("Result: %s configured\n", target)
	} else {
		fmt.Println("Already configured, no changes needed.")
	}

	if agentCfgExec {
		return execClaude(target, status, projectDir, orgDir, args)
	}

	return nil
}

func printAuthSources(s agent.AuthStatus) {
	if val := os.Getenv("ANTHROPIC_API_KEY"); val != "" {
		fmt.Printf("  ANTHROPIC_API_KEY:            %s\n", maskEnvVar(val))
	} else if s.HasSecretAPI {
		fmt.Printf("  ANTHROPIC_API_KEY:            %s\n", s.SecretAPIInfo)
	} else {
		fmt.Println("  ANTHROPIC_API_KEY:            (not set)")
	}

	if val := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); val != "" {
		if val[0] == '{' {
			fmt.Printf("  CLAUDE_CODE_OAUTH_TOKEN:      set (JSON, %d chars)\n", len(val))
		} else {
			fmt.Printf("  CLAUDE_CODE_OAUTH_TOKEN:      %s\n", maskEnvVar(val))
		}
	} else if s.HasSecretOAuth {
		fmt.Printf("  CLAUDE_CODE_OAUTH_TOKEN:      %s\n", s.SecretOAuthInfo)
	} else {
		fmt.Println("  CLAUDE_CODE_OAUTH_TOKEN:      (not set)")
	}

	if s.HasRefreshToken() {
		fmt.Printf("  OAUTH_REFRESH_TOKEN:          %s\n", s.RefreshTokenInfo)
	} else {
		fmt.Println("  OAUTH_REFRESH_TOKEN:          (not set)")
	}

	if s.HasCredFile {
		fmt.Println("  Credentials file:             present")
	} else {
		fmt.Println("  Credentials file:             absent")
	}

	if s.HasKeychain {
		fmt.Println("  macOS Keychain:               present")
	}

	fmt.Println()
}

func saveRefreshToken(status agent.AuthStatus, projectDir, orgDir string) error {
	token := agent.ExtractRefreshToken(status.ConfigDir)
	if token == "" {
		fmt.Println("No refresh token found in " + status.ConfigDir + "/.credentials.json")
		fmt.Println()
		fmt.Println("To get one, do a browser login first:")
		fmt.Println("  ateam agent-config --setup-interactive")
		fmt.Println("  # Complete the browser login, then /exit")
		fmt.Println("  ateam agent-config --save-refresh-token")
		return fmt.Errorf("no refresh token available")
	}
	if err := storeRefreshToken(token, projectDir, orgDir); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Refresh token saved to ateam secrets (container-local).")
	fmt.Fprintln(os.Stderr, "  Verify: ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --get")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "On any new container, run:")
	fmt.Fprintln(os.Stderr, "  ateam agent-config --setup-interactive")
	fmt.Fprintln(os.Stderr, "")
	return nil
}

func storeRefreshToken(token, projectDir, orgDir string) error {
	backend := secret.DefaultBackend()
	resolver := secret.NewResolver(projectDir, orgDir, backend, nil)
	scope := resolver.ScopeForName(secret.ScopeGlobal)

	var err error
	if backend == secret.BackendKeychain {
		err = secret.KeychainSet(secret.KeychainAccount(scope.Name, scope.KeychainKey, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN"), token)
	} else {
		store := &secret.FileStore{Path: scope.EnvFile}
		err = store.Set("CLAUDE_CODE_OAUTH_REFRESH_TOKEN", token)
	}
	if err != nil {
		return fmt.Errorf("failed to save refresh token: %w", err)
	}
	return nil
}

func maskToken(val string) string {
	if len(val) <= 12 {
		return "***"
	}
	return val[:8] + "***" + val[len(val)-4:]
}

func lookupEnvOptional() (*root.ResolvedEnv, error) {
	return root.Lookup()
}

func execClaude(target agent.AuthMethod, status agent.AuthStatus, projectDir, orgDir string, extraArgs []string) error {
	binary, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	refreshToken := agent.ResolveRefreshToken(status.ConfigDir, projectDir, orgDir)

	if refreshToken != "" {
		fmt.Println("\nBootstrapping credentials from refresh token...")
		loginEnv := agent.BuildCleanEnv(target, refreshToken)
		loginCmd := exec.Command(binary, "auth", "login")
		loginCmd.Env = loginEnv
		loginCmd.Stdout = os.Stdout
		loginCmd.Stderr = os.Stderr
		if err := loginCmd.Run(); err != nil {
			return fmt.Errorf("refresh token login failed: %w", err)
		}
	}

	argv := append([]string{"claude"}, extraArgs...)
	env := agent.BuildCleanEnv(target, "")

	fmt.Printf("Exec: %s %v\n", binary, extraArgs)
	return syscall.Exec(binary, argv, env)
}
