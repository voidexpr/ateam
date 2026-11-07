package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/secret"
	"github.com/spf13/cobra"
)

// defaultSharedClaudePath is the agent config dir under the user's .ateam/
// shared with Linux containers (see ISOLATION.md). Relative to the home dir.
const defaultSharedClaudePath = "state/agents/claude-linux"
const ateamContainerBinPath = "/usr/local/bin/ateam"

// claudeEssentials lists the files and directories inside .claude/ that are
// worth copying between environments. Everything else (sessions, projects,
// cache, history, shell-snapshots, etc.) is ephemeral or machine-specific.
var claudeEssentials = []string{
	".credentials.json",
	"settings.json",
	"skills",
	"plugins",
	"hooks",
	"backups",
}

var (
	agentCfgAudit bool

	agentCfgCopyOut        bool
	agentCfgCopyIn         bool
	agentCfgContainer      string
	agentCfgPath           string
	agentCfgHome           string
	agentCfgForce          bool
	agentCfgCopyAteam      bool
	agentCfgDryRun         bool
	agentCfgEssentialsOnly bool
)

var agentConfigCmd = &cobra.Command{
	Use:   "agent-config [-- AGENT_ARGS...]",
	Short: "[experimental] Configure Claude Code agent authentication",
	Long: `[experimental] Configure Claude Code agent authentication.

Audit auth state (default), set up interactive sessions, and copy config
between host and containers.

AUDIT (default, works everywhere, read-only):
  ateam agent-config
  ateam agent-config --audit
  ateam agent-config --audit --container my-app    # remote audit

COPY CONFIG OUT OF A CONTAINER:
  ateam agent-config --copy-out --container my-app
  ateam agent-config --copy-out --container my-app --path /tmp/claude-config

COPY CONFIG INTO A CONTAINER:
  ateam agent-config --copy-in --container my-app
  ateam agent-config --copy-in --container my-app --force --copy-ateam

To log into the config dir an unattended agent uses, run 'ateam login'.`,
	Args: cobra.ArbitraryArgs,
	RunE: runAgentConfig,
}

func init() {
	agentConfigCmd.Flags().BoolVar(&agentCfgAudit, "audit", false, "show auth state, tokens, and export instructions")
	agentConfigCmd.Flags().BoolVar(&agentCfgCopyOut, "copy-out", false, "copy agent config from a container to a local directory")
	agentConfigCmd.Flags().BoolVar(&agentCfgCopyIn, "copy-in", false, "copy agent config into a container from a local directory")
	agentConfigCmd.Flags().StringVar(&agentCfgContainer, "container", "", "target container name (for --copy-out, --copy-in, --audit)")
	agentConfigCmd.Flags().StringVar(&agentCfgPath, "path", "", "local directory for agent config (default: <~/.ateam>/"+defaultSharedClaudePath+")")
	agentConfigCmd.Flags().StringVar(&agentCfgHome, "home", "", "override container home directory (auto-detected by default)")
	agentConfigCmd.Flags().BoolVar(&agentCfgForce, "force", false, "overwrite existing config in container (for --copy-in)")
	agentConfigCmd.Flags().BoolVar(&agentCfgCopyAteam, "copy-ateam", false, "also copy ateam linux binary into the container (for --copy-in)")
	agentConfigCmd.Flags().BoolVar(&agentCfgDryRun, "dry-run", false, "show what would be copied without executing")
	agentConfigCmd.Flags().BoolVar(&agentCfgEssentialsOnly, "essentials-only", false, "copy only essential files (credentials, settings, plugins, skills, hooks, backups)")
}

func runAgentConfig(cmd *cobra.Command, args []string) error {
	var projectDir, homeDir string
	if env, err := lookupEnvOptional(); err == nil {
		projectDir = env.ProjectDir
		homeDir = env.HomeDir
	}

	// Resolve partial container names before any subcommand uses them.
	if agentCfgContainer != "" {
		resolved, err := resolveContainerName(agentCfgContainer)
		if err != nil {
			return err
		}
		agentCfgContainer = resolved
	}

	// --copy-out / --copy-in require --container
	if agentCfgCopyOut {
		return runCopyOut(agentCfgContainer, agentCfgPath, agentCfgHome, homeDir, agentCfgDryRun, agentCfgEssentialsOnly)
	}
	if agentCfgCopyIn {
		return runCopyIn(agentCfgContainer, agentCfgPath, agentCfgHome, agentCfgForce, agentCfgCopyAteam, homeDir, agentCfgDryRun, agentCfgEssentialsOnly)
	}

	// --audit works everywhere, no container-only check.
	// With --container, run audit remotely inside the container.
	if agentCfgAudit {
		if agentCfgContainer != "" {
			return runRemoteAudit(agentCfgContainer, agentCfgHome)
		}
		return runAgentConfigAudit(projectDir, homeDir)
	}

	// No action flags → default to audit.
	if agentCfgContainer != "" {
		return runRemoteAudit(agentCfgContainer, agentCfgHome)
	}
	return runAgentConfigAudit(projectDir, homeDir)
}

func runAgentConfigAudit(projectDir, homeDir string) error {
	fmt.Println("[experimental] Claude Code Agent Configuration Audit")
	fmt.Println()

	status := agent.DetectAuth(projectDir)

	fmt.Printf("Config dir:       %s\n", status.ConfigDir)
	fmt.Printf("Active auth:      %s\n", status.Active)
	fmt.Println()

	printAuthSources(status)

	claudeStatus := runClaudeAuthStatus("")
	fmt.Println("Claude CLI (claude auth status):")
	if claudeStatus.err != nil {
		fmt.Printf("  (could not run 'claude auth status': %v)\n", claudeStatus.err)
	} else {
		fmt.Printf("  Logged in:    %v\n", claudeStatus.loggedIn)
		fmt.Printf("  Auth method:  %s\n", claudeStatus.authMethod)
		if claudeStatus.apiProvider != "" {
			fmt.Printf("  API provider: %s\n", claudeStatus.apiProvider)
		}
	}
	fmt.Println()

	// Mismatch detection.
	if claudeStatus.err == nil {
		if !claudeStatus.loggedIn && (status.HasOAuth || status.HasAPIKey) {
			fmt.Println("  Warning: ateam detects auth tokens but claude reports not logged in.")
			fmt.Println("  The token may be expired or invalid.")
			fmt.Println()
		}
		if claudeStatus.loggedIn && status.Active == agent.AuthNone {
			fmt.Println("  Warning: claude reports logged in but ateam detects no auth source.")
			fmt.Println("  Claude may be using credentials not visible to ateam (e.g., macOS Keychain).")
			fmt.Println()
		}
	}

	refreshToken := agent.ExtractRefreshToken(status.ConfigDir)
	if refreshToken != "" {
		fmt.Println("Interactive session detected (refresh token in .credentials.json).")
		fmt.Println()
	}

	// Check shared config dir (for container use with CLAUDE_CONFIG_DIR).
	if homeDir != "" {
		printSharedConfigStatus(homeDir)
	}

	return nil
}

type claudeAuthResult struct {
	loggedIn    bool
	authMethod  string
	apiProvider string
	err         error
}

// runClaudeAuthStatus runs "claude auth status --json".
// If configDir is non-empty, sets CLAUDE_CONFIG_DIR for the subprocess.
func runClaudeAuthStatus(configDir string) claudeAuthResult {
	binary, err := exec.LookPath("claude")
	if err != nil {
		return claudeAuthResult{err: fmt.Errorf("claude not found in PATH")}
	}

	jsonCmd := exec.Command(binary, "auth", "status", "--json")
	if configDir != "" {
		jsonCmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+configDir)
	}
	// claude auth status exits 1 when not logged in but still outputs valid JSON.
	jsonOut, err := jsonCmd.Output()
	if err != nil && len(jsonOut) == 0 {
		return claudeAuthResult{err: fmt.Errorf("command failed: %w", err)}
	}

	var parsed struct {
		LoggedIn    bool   `json:"loggedIn"`
		AuthMethod  string `json:"authMethod"`
		APIProvider string `json:"apiProvider"`
	}
	if err := json.Unmarshal(jsonOut, &parsed); err != nil {
		return claudeAuthResult{err: fmt.Errorf("unexpected output: %s", strings.TrimSpace(string(jsonOut)))}
	}

	return claudeAuthResult{
		loggedIn:    parsed.LoggedIn,
		authMethod:  parsed.AuthMethod,
		apiProvider: parsed.APIProvider,
	}
}

func printAuthSources(s agent.AuthStatus) {
	if val := os.Getenv("ANTHROPIC_API_KEY"); val != "" {
		fmt.Printf("  ANTHROPIC_API_KEY:            %s\n", secret.MaskValue(val))
	} else if s.HasSecretAPI {
		fmt.Printf("  ANTHROPIC_API_KEY:            %s\n", s.SecretAPIInfo)
	} else {
		fmt.Println("  ANTHROPIC_API_KEY:            (not set)")
	}

	if val := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); val != "" {
		if val[0] == '{' {
			fmt.Printf("  CLAUDE_CODE_OAUTH_TOKEN:      set (JSON, %d chars)\n", len(val))
		} else {
			fmt.Printf("  CLAUDE_CODE_OAUTH_TOKEN:      %s\n", secret.MaskValue(val))
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

func printSharedConfigStatus(homeDir string) {
	sharedDir := filepath.Join(homeDir, defaultSharedClaudePath)
	info, err := os.Stat(sharedDir)
	if err != nil || !info.IsDir() {
		fmt.Printf("Shared config:    %s (not found)\n", sharedDir)
		fmt.Println("  Run 'mkdir -p " + sharedDir + "' to create it")
		fmt.Println()
		return
	}

	fmt.Printf("Shared config:    %s\n", sharedDir)

	credPath := filepath.Join(sharedDir, ".credentials.json")
	if _, err := os.Stat(credPath); err == nil {
		refreshToken := agent.ExtractRefreshToken(sharedDir)
		if refreshToken != "" {
			fmt.Println("  Credentials:    present (has refresh token)")
		} else {
			fmt.Println("  Credentials:    present (no refresh token)")
		}
	} else {
		fmt.Println("  Credentials:    absent")
	}

	claudeJSON := filepath.Join(sharedDir, ".claude.json")
	if _, err := os.Stat(claudeJSON); err == nil {
		fmt.Println("  .claude.json:   present")
	} else {
		fmt.Println("  .claude.json:   absent")
	}

	settingsJSON := filepath.Join(sharedDir, "settings.json")
	if _, err := os.Stat(settingsJSON); err == nil {
		fmt.Println("  settings.json:  present")
	} else {
		fmt.Println("  settings.json:  absent")
	}

	sharedStatus := runClaudeAuthStatus(sharedDir)
	if sharedStatus.err != nil {
		fmt.Printf("  claude auth:    %v\n", sharedStatus.err)
	} else if sharedStatus.loggedIn {
		fmt.Printf("  claude auth:    logged in (%s)\n", sharedStatus.authMethod)
	} else {
		fmt.Println("  claude auth:    not logged in")
	}

	fmt.Println()
}

func lookupEnvOptional() (*root.ResolvedEnv, error) {
	return root.Lookup("", "")
}

// ---------------------------------------------------------------------------
// Docker container helpers
// ---------------------------------------------------------------------------

type containerInfo struct {
	home      string
	user      string
	configDir string // CLAUDE_CONFIG_DIR, empty if unset
}

// claudePaths returns the claude config directory and .claude.json path
// for the container. When CLAUDE_CONFIG_DIR is set, claudeJSON is empty.
func (ci containerInfo) claudePaths() (claudeDir, claudeJSON string) {
	if ci.configDir != "" {
		return ci.configDir, ""
	}
	return ci.home + "/.claude", ci.home + "/.claude.json"
}

// detectContainer gathers home, user, and CLAUDE_CONFIG_DIR in a single docker exec.
func detectContainer(container, homeOverride string) (containerInfo, error) {
	if homeOverride != "" {
		user, _ := dockerExecOutput(container, "id", "-un")
		if user == "" {
			user = "root"
		}
		configDir, _ := dockerExecOutput(container, "sh", "-c", "echo $CLAUDE_CONFIG_DIR")
		return containerInfo{home: homeOverride, user: user, configDir: configDir}, nil
	}
	out, err := dockerExecOutput(container, "sh", "-c", "echo $HOME; id -un; echo $CLAUDE_CONFIG_DIR")
	if err != nil {
		return containerInfo{}, fmt.Errorf("cannot detect container environment: %w", err)
	}
	lines := strings.SplitN(out, "\n", 3)
	info := containerInfo{user: "root"}
	if len(lines) >= 1 {
		info.home = lines[0]
	}
	if len(lines) >= 2 && lines[1] != "" {
		info.user = lines[1]
	}
	if len(lines) >= 3 {
		info.configDir = lines[2]
	}
	if info.home == "" {
		return containerInfo{}, fmt.Errorf("container %s has empty $HOME", container)
	}
	return info, nil
}

// copyAteamBinary copies the ateam linux binary into a container.
func copyAteamBinary(containerName, homeDir string) error {
	binary := findLinuxBinary(homeDir)
	if binary == "" {
		return fmt.Errorf("no linux ateam binary found (run 'make companion' to build one)")
	}
	target := containerName + ":" + ateamContainerBinPath
	fmt.Printf("Copying %s → %s\n", binary, target)
	if err := dockerCp(binary, target); err != nil {
		return err
	}
	fmt.Println("Done.")
	return nil
}

func resolveLocalPath(flagPath, homeDir string) (string, error) {
	path := flagPath
	if path == "" {
		if homeDir == "" {
			return "", nil
		}
		path = filepath.Join(homeDir, defaultSharedClaudePath)
	}
	if err := validateLocalPath(path); err != nil {
		return "", err
	}
	return path, nil
}

// validateLocalPath refuses paths that would risk overwriting the user's
// own ~/.claude or ~/.claude.json. This is critical — --path should only
// ever point to a dedicated shared-config directory, never to $HOME or
// the default claude config location.
func validateLocalPath(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot validate path: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot validate path: %w", err)
	}
	abs = filepath.Clean(abs)
	// Resolve symlinks so a link to $HOME or ~/.claude is also caught.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	home = filepath.Clean(home)
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}

	if abs == home {
		return fmt.Errorf("--path must not be your home directory (%s) — this would overwrite ~/.claude and ~/.claude.json", home)
	}
	claudeDir := filepath.Join(home, ".claude")
	if resolved, err := filepath.EvalSymlinks(claudeDir); err == nil {
		claudeDir = resolved
	}
	if abs == claudeDir {
		return fmt.Errorf("--path must not be ~/.claude — this would pollute your Claude config directory")
	}
	return nil
}

func printLocalDirStatus(path string) {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Printf("  target %s does not exist (will create)\n", path)
		return
	}
	if !info.IsDir() {
		fmt.Printf("  target %s exists but is not a directory\n", path)
		return
	}
	entries, _ := os.ReadDir(path)
	if len(entries) == 0 {
		fmt.Printf("  target %s exists (empty)\n", path)
	} else {
		fmt.Printf("  target %s exists (%d entries)\n", path, len(entries))
	}
}

func containerPathExists(container, path string) bool {
	cmd := exec.Command("docker", "exec", container, "test", "-e", path)
	return cmd.Run() == nil
}

func containerDirEmpty(container, path string) bool {
	out, err := dockerExecOutput(container, "sh", "-c", `ls -A "$1" 2>/dev/null`, "_", path)
	return err == nil && strings.TrimSpace(out) == ""
}

// ---------------------------------------------------------------------------
// --copy-out: container → host
// ---------------------------------------------------------------------------

func runCopyOut(containerName, flagPath, homeOverride, homeDir string, dryRun, essentialsOnly bool) error {
	if containerName == "" {
		return fmt.Errorf("--container is required with --copy-out")
	}

	localPath, err := resolveLocalPath(flagPath, homeDir)
	if err != nil {
		return err
	}
	if localPath == "" {
		return fmt.Errorf("--path is required (no user ateam directory found for the default)")
	}

	ci, err := detectContainer(containerName, homeOverride)
	if err != nil {
		return err
	}

	claudeDir, claudeJSON := ci.claudePaths()

	if !containerPathExists(containerName, claudeDir) {
		return fmt.Errorf("%s does not exist in container %s", claudeDir, containerName)
	}

	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}

	localClaudeDir := filepath.Join(localPath, ".claude")
	mode := "all"
	if essentialsOnly {
		mode = "essentials-only"
	}
	fmt.Printf("%sCopying from container %s (%s) → %s [%s]\n\n", prefix, containerName, ci.home, localPath, mode)

	if dryRun {
		printLocalDirStatus(localClaudeDir)
	}

	if essentialsOnly {
		for _, name := range claudeEssentials {
			src := claudeDir + "/" + name
			if containerPathExists(containerName, src) {
				fmt.Printf("  %s%s → %s\n", prefix, src, filepath.Join(localClaudeDir, name))
			}
		}
	} else {
		fmt.Printf("  %s%s/ → %s/\n", prefix, claudeDir, localClaudeDir)
	}

	if claudeJSON != "" && containerPathExists(containerName, claudeJSON) {
		fmt.Printf("  %s%s → %s\n", prefix, claudeJSON, filepath.Join(localPath, ".claude.json"))
	} else if claudeJSON != "" {
		fmt.Println("  .claude.json not found in container (skip)")
	}

	if dryRun {
		return nil
	}

	if err := os.MkdirAll(localClaudeDir, 0755); err != nil {
		return err
	}

	if essentialsOnly {
		for _, name := range claudeEssentials {
			src := claudeDir + "/" + name
			if !containerPathExists(containerName, src) {
				continue
			}
			dst := filepath.Join(localClaudeDir, name)
			if err := dockerCp(containerName+":"+src, dst); err != nil {
				return err
			}
		}
	} else {
		if err := dockerCp(containerName+":"+claudeDir+"/.", localClaudeDir+"/"); err != nil {
			return err
		}
	}

	if claudeJSON != "" && containerPathExists(containerName, claudeJSON) {
		if err := dockerCp(containerName+":"+claudeJSON, filepath.Join(localPath, ".claude.json")); err != nil {
			return err
		}
	}

	// secrets.env is NOT copied — it is manually maintained and contains
	// the OAuth token from 'claude setup-token'.
	if _, err := os.Stat(filepath.Join(localPath, "secrets.env")); os.IsNotExist(err) {
		fmt.Println()
		fmt.Println("  Note: no secrets.env in local path.")
		fmt.Println("  If headless agents are needed, generate an OAuth token inside the container:")
		fmt.Println("    claude setup-token")
		fmt.Println("  Then save it:")
		fmt.Println("    ateam secret CLAUDE_CODE_OAUTH_TOKEN --scope org --set")
	}

	fmt.Println()
	fmt.Println("Done.")
	return nil
}

// ---------------------------------------------------------------------------
// --copy-in: host → container
// ---------------------------------------------------------------------------

func runCopyIn(containerName, flagPath, homeOverride string, force, copyAteam bool, homeDir string, dryRun, essentialsOnly bool) error {
	if containerName == "" {
		return fmt.Errorf("--container is required with --copy-in")
	}

	localPath, err := resolveLocalPath(flagPath, homeDir)
	if err != nil {
		return err
	}
	if localPath == "" {
		return fmt.Errorf("--path is required (no user ateam directory found for the default)")
	}

	localClaudeDir := filepath.Join(localPath, ".claude")
	if info, err := os.Stat(localClaudeDir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s does not exist — nothing to copy", localClaudeDir)
	}

	ci, err := detectContainer(containerName, homeOverride)
	if err != nil {
		return err
	}

	claudeDir, claudeJSON := ci.claudePaths()

	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}

	mode := "all"
	if essentialsOnly {
		mode = "essentials-only"
	}
	fmt.Printf("%sCopying from %s → container %s (%s, user=%s) [%s]\n\n", prefix, localPath, containerName, ci.home, ci.user, mode)

	claudeDirExists := containerPathExists(containerName, claudeDir)
	claudeDirNonEmpty := claudeDirExists && !containerDirEmpty(containerName, claudeDir)
	if claudeDirNonEmpty {
		if !force && !dryRun {
			return fmt.Errorf("%s already exists in container %s (use --force to overwrite)", claudeDir, containerName)
		}
		if dryRun && !force {
			fmt.Printf("  target %s exists in container (would need --force)\n", claudeDir)
		}
		if force && !essentialsOnly {
			fmt.Printf("  %sclear existing %s\n", prefix, claudeDir)
		}
	} else if !claudeDirExists {
		fmt.Printf("  %screate %s\n", prefix, claudeDir)
	}

	if essentialsOnly {
		for _, name := range claudeEssentials {
			src := filepath.Join(localClaudeDir, name)
			if _, err := os.Stat(src); err == nil {
				fmt.Printf("  %s%s → %s/%s\n", prefix, src, claudeDir, name)
			}
		}
	} else {
		fmt.Printf("  %s%s/ → %s/\n", prefix, localClaudeDir, claudeDir)
	}

	localClaudeJSON := filepath.Join(localPath, ".claude.json")
	if claudeJSON != "" {
		if _, err := os.Stat(localClaudeJSON); err == nil {
			fmt.Printf("  %s%s → %s\n", prefix, localClaudeJSON, claudeJSON)
		}
	}

	localSecrets := filepath.Join(localPath, "secrets.env")
	if _, err := os.Stat(localSecrets); err == nil {
		ateamHomeDir := ci.home + "/.ateam"
		fmt.Printf("  %s%s → %s/secrets.env\n", prefix, localSecrets, ateamHomeDir)
	}

	if copyAteam {
		binary := findLinuxBinary(homeDir)
		if binary != "" {
			fmt.Printf("  %s%s → %s:%s\n", prefix, binary, containerName, ateamContainerBinPath)
		} else {
			fmt.Printf("  %sateam binary: not found (run 'make companion')\n", prefix)
		}
	}

	if dryRun {
		return nil
	}

	if _, err := dockerExecOutput(containerName, "mkdir", "-p", claudeDir); err != nil {
		return fmt.Errorf("creating %s in container: %w", claudeDir, err)
	}

	if essentialsOnly {
		for _, name := range claudeEssentials {
			src := filepath.Join(localClaudeDir, name)
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if err := dockerCp(src, containerName+":"+claudeDir+"/"+name); err != nil {
				return err
			}
		}
	} else {
		if claudeDirNonEmpty && force {
			_, _ = dockerExecOutput(containerName, "sh", "-c",
				`rm -rf "$1"/* "$1"/.[!.]* 2>/dev/null || true`, "_", claudeDir)
		}
		if err := dockerCp(localClaudeDir+"/.", containerName+":"+claudeDir+"/"); err != nil {
			return err
		}
	}

	chownPaths := []string{claudeDir}

	if claudeJSON != "" {
		if _, err := os.Stat(localClaudeJSON); err == nil {
			if err := dockerCp(localClaudeJSON, containerName+":"+claudeJSON); err != nil {
				return err
			}
			chownPaths = append(chownPaths, claudeJSON)
		}
	}

	if _, err := os.Stat(localSecrets); err == nil {
		ateamHomeDir := ci.home + "/.ateam"
		if _, err := dockerExecOutput(containerName, "mkdir", "-p", ateamHomeDir); err != nil {
			return fmt.Errorf("creating %s in container: %w", ateamHomeDir, err)
		}
		if err := dockerCp(localSecrets, containerName+":"+ateamHomeDir+"/secrets.env"); err != nil {
			return err
		}
		chownPaths = append(chownPaths, ateamHomeDir)
	}

	chownArgs := append([]string{"sh", "-c", `u=$1; shift; chown -R "$u":"$u" "$@" 2>/dev/null || true`, "_", ci.user}, chownPaths...)
	_, _ = dockerExecOutput(containerName, chownArgs...)

	if copyAteam {
		if err := copyAteamBinary(containerName, homeDir); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Println("Done.")
	return nil
}

// ---------------------------------------------------------------------------
// --audit --container: remote audit via docker exec
// ---------------------------------------------------------------------------

func runRemoteAudit(containerName, homeOverride string) error {
	ci, err := detectContainer(containerName, homeOverride)
	if err != nil {
		return err
	}

	fmt.Printf("Remote audit: container %s (home=%s)\n\n", containerName, ci.home)

	// Try ateam agent-config --audit inside the container
	cmd := exec.Command("docker", "exec", containerName, "ateam", "agent-config", "--audit")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// ateam not available — fall back to claude auth status
		fmt.Println("  (ateam not found in container, falling back to claude auth status)")
		fmt.Println()

		out, err2 := dockerExecOutput(containerName, "claude", "auth", "status", "--text")
		if err2 != nil {
			return fmt.Errorf("neither ateam nor claude found in container %s", containerName)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
	}
	return nil
}
