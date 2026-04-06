package cmd

import (
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"syscall"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

var (
	agentAuthMethod           string
	agentAuthExec             bool
	agentAuthDryRun           bool
	agentAuthContainerOnly    bool
	agentAuthWipeConfigClean  bool
	agentAuthSaveRefreshToken bool
	agentAuthRefreshToken     string
)

var agentAuthCmd = &cobra.Command{
	Use:   "agent-auth [-- AGENT_ARGS...]",
	Short: "Configure Claude Code authentication",
	Long: `Configure Claude Code authentication, primarily inside Docker containers.

Detects the current auth state, cleans up conflicting files and caches,
and optionally execs claude with a clean environment.

SETUP (one-time, in a container with persistent ~/.claude volume):

  1. Do a browser login:
       ateam agent-auth --method regular --exec
       # Claude starts → complete browser login → /exit

  2. Save the refresh token to ateam secrets:
       ateam agent-auth --save-refresh-token

  The refresh token is NOT the token shown in the browser. It is
  extracted from ~/.claude/.credentials.json after a successful login.

USE (any new container, no browser needed):

  ateam agent-auth --method regular --exec
  # Bootstraps credentials from saved refresh token, then starts claude

HEADLESS (for -p mode with CLAUDE_CODE_OAUTH_TOKEN):

  ateam agent-auth --method oauth --exec -- -p "hello"`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgentAuth,
}

func init() {
	agentAuthCmd.Flags().StringVar(&agentAuthMethod, "method", "", "target auth method: oauth, api, or regular (required)")
	agentAuthCmd.Flags().BoolVar(&agentAuthExec, "exec", false, "exec claude after configuring auth (replaces process)")
	agentAuthCmd.Flags().BoolVarP(&agentAuthDryRun, "dry-run", "n", false, "show what would be done without making changes")
	agentAuthCmd.Flags().BoolVar(&agentAuthContainerOnly, "container-only", true, "only run inside Docker containers")
	agentAuthCmd.Flags().BoolVar(&agentAuthWipeConfigClean, "wipe-config-clean", false, "also remove plugins, skills, and other config (keeps only settings.json)")
	agentAuthCmd.Flags().BoolVar(&agentAuthSaveRefreshToken, "save-refresh-token", false, "extract refresh token from .credentials.json and save to ateam secrets")
	agentAuthCmd.Flags().StringVar(&agentAuthRefreshToken, "refresh-token", "", "provide a refresh token from .credentials.json (not the browser URL token)")
}

func runAgentAuth(cmd *cobra.Command, args []string) error {
	if agentAuthContainerOnly && !isInContainer() {
		return fmt.Errorf("agent-auth is designed to run inside Docker containers (use --container-only=false to override)")
	}

	if agentAuthWipeConfigClean && goruntime.GOOS != "linux" {
		return fmt.Errorf("--wipe-config-clean is only allowed on Linux (current OS: %s)", goruntime.GOOS)
	}

	if agentAuthExec && agentAuthDryRun {
		return fmt.Errorf("--exec and --dry-run are incompatible")
	}

	// Try to discover project/org for secret resolution, but don't require them.
	var projectDir, orgDir string
	if env, err := lookupEnvOptional(); err == nil {
		projectDir = env.ProjectDir
		orgDir = env.OrgDir
	}

	// If --refresh-token provided, save it to ateam secrets first.
	if agentAuthRefreshToken != "" {
		if err := storeRefreshToken(agentAuthRefreshToken, projectDir, orgDir); err != nil {
			return err
		}
		fmt.Println("Refresh token saved to ateam secrets.")
	}

	status := agent.DetectAuth(projectDir, orgDir)

	// Handle --save-refresh-token independently of --method.
	if agentAuthSaveRefreshToken {
		return saveRefreshToken(status, projectDir, orgDir)
	}

	if agentAuthMethod == "" {
		return fmt.Errorf("--method is required (oauth, api, or regular)")
	}
	target, ok := agent.ParseAuthMethod(agentAuthMethod)
	if !ok {
		return fmt.Errorf("invalid --method %q (use oauth, api, or regular)", agentAuthMethod)
	}

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

	// Always clean stale auth state, even when active matches target.
	results := agent.Cleanup(status.ConfigDir, agentAuthWipeConfigClean, agentAuthDryRun)
	results = append(results, agent.EnsureClaudeState(status.ConfigDir, agentAuthDryRun))

	// Only print actions if there's something to report.
	hasActions := false
	for _, r := range results {
		if r.Action != "skip" {
			hasActions = true
			break
		}
	}

	if hasActions {
		if agentAuthDryRun {
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

	if agentAuthDryRun {
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

	if agentAuthExec {
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
		fmt.Println("  ateam agent-auth --method regular --exec")
		fmt.Println("  # Complete the browser login, then /exit")
		fmt.Println("  ateam agent-auth --save-refresh-token")
		return fmt.Errorf("no refresh token available")
	}
	if err := storeRefreshToken(token, projectDir, orgDir); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Refresh token saved to ateam secrets (container-local).")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To save on the host, copy this token:")
	fmt.Fprintln(os.Stderr, "  ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set --value 'TOKEN'")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Or pipe from extract script:")
	fmt.Fprintln(os.Stderr, "  ./test/docker-auth/extract-refresh-token.sh --volume VOLUME | ateam secret CLAUDE_CODE_OAUTH_REFRESH_TOKEN --set")
	fmt.Fprintln(os.Stderr, "")
	// Print raw token to stdout so it can be piped
	fmt.Print(token)
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

func lookupEnvOptional() (*root.ResolvedEnv, error) {
	return root.Lookup()
}

func isInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func execClaude(target agent.AuthMethod, status agent.AuthStatus, projectDir, orgDir string, extraArgs []string) error {
	binary, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}

	refreshToken := agent.ResolveRefreshToken(status.ConfigDir, projectDir, orgDir)

	// If we have a refresh token, run the login step first.
	// Claude's refresh token flow exchanges the token, stores credentials,
	// then exits with "Login successful." — it does NOT start a session.
	// So we run it as a subprocess first, then exec the actual session.
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

	// Now exec the actual session without the refresh token env vars.
	argv := append([]string{"claude"}, extraArgs...)
	env := agent.BuildCleanEnv(target, "")

	fmt.Printf("Exec: %s %v\n", binary, extraArgs)
	return syscall.Exec(binary, argv, env)
}
