package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/streamutil"
	"github.com/spf13/cobra"
)

var (
	resumeLast   bool
	resumeLaunch bool
)

var resumeCmd = &cobra.Command{
	Use:   "resume [EXEC_ID]",
	Short: "Resume an interactive agent session from a previous run",
	Long: `Look up the session id from a previous run's stream file and either
print the resume command or launch the agent's resume directly.

The resumed session is interactive and runs outside ateam: it gets no
agent_execs row, no sandbox settings, and no tracking. It picks up from
the original run's last turn.

Resume is supported for "claude", "codex", and "codex-tmux" runs whose
container is "none". For docker / docker-exec runs the session id is
still printed but --launch is refused since the session state lives
inside (or with) the container, not on the host.

The binary used for resume can be overridden via env vars:
  ATEAM_RESUME_CLAUDE_CMD  (default: "claude")
  ATEAM_RESUME_CODEX_CMD   (default: "codex"; used for codex and codex-tmux)
Each may include extra arguments (e.g. "my-claude --foo") which are
parsed with strings.Fields and prepended to the resume args.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runResume,
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeLast, "last", false, "resume the most recent claude, codex, or codex-tmux run")
	resumeCmd.Flags().BoolVar(&resumeLaunch, "launch", false, "exec into the resume command instead of just printing it")
	rootCmd.AddCommand(resumeCmd)
}

// Env vars overriding the binary (and optional leading args) used by
// `ateam resume --launch`. Parsed via strings.Fields, so "my-claude --foo"
// becomes the binary "my-claude" with prefix arg "--foo".
const (
	envResumeClaudeCmd = "ATEAM_RESUME_CLAUDE_CMD"
	envResumeCodexCmd  = "ATEAM_RESUME_CODEX_CMD"
)

// resumableAgents lists the agent names whose runs can be resumed. Codex
// and codex-tmux share the same on-disk session id and the same `codex`
// CLI, so they're handled identically below.
var resumableAgents = []string{
	agent.NameClaude,
	agent.NameCodex,
	agent.NameCodexTmux,
}

func isResumableAgent(name string) bool {
	for _, a := range resumableAgents {
		if a == name {
			return true
		}
	}
	return false
}

func runResume(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && !resumeLast {
		return fmt.Errorf("specify an exec ID or --last")
	}
	if len(args) > 0 && resumeLast {
		return fmt.Errorf("--last cannot be combined with an exec ID")
	}

	env, err := resolveEnv()
	if err != nil {
		return fmt.Errorf("cannot find project: %w", err)
	}
	db, err := requireProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()

	row, err := selectResumeRow(db, args)
	if err != nil {
		return err
	}
	if !isResumableAgent(row.Agent) {
		return fmt.Errorf("resume only supports %s (run %d used agent %q)",
			strings.Join(resumableAgents, ", "), row.ID, row.Agent)
	}
	if row.StreamFile == "" {
		return fmt.Errorf("run %d has no stream file recorded", row.ID)
	}

	streamPath := root.ResolveStreamPath(env.ProjectDir, env.OrgDir, row.StreamFile)
	sessionID, err := extractSessionID(streamPath)
	if err != nil {
		return fmt.Errorf("cannot read session id from %s: %w", streamPath, err)
	}
	if sessionID == "" {
		return fmt.Errorf("no session id found in %s (run may not have started)", streamPath)
	}

	execMD := cmdMDPath(streamPath)
	configDir, configDirSource := resolveResumeConfigDir(execMD, env, row)

	fmt.Printf("Run:        %d (%s/%s)\n", row.ID, row.Role, row.Action)
	if row.Profile != "" {
		fmt.Printf("Profile:    %s\n", row.Profile)
	}
	if row.Container != "" && row.Container != "none" {
		container := row.Container
		if row.ContainerID != "" {
			container += " (" + row.ContainerID + ")"
		}
		fmt.Printf("Container:  %s\n", container)
	}
	fmt.Printf("Started:    %s\n", row.StartedAt)
	fmt.Printf("Session:    %s\n", sessionID)
	if configDir != "" {
		fmt.Printf("CLAUDE_CONFIG_DIR: %s (%s)\n", configDir, configDirSource)
	}
	fmt.Println()

	hostCmd, hostArgs := resumeCommand(row.Agent, sessionID)
	hostCmdLine := joinCmd(hostCmd, hostArgs)
	// Prepend CLAUDE_CONFIG_DIR so the printed command is copy-pasteable; the
	// informational line above is easy to miss and resuming with the wrong
	// config dir silently picks up a different agent profile.
	hostCmdLineWithEnv := hostCmdLine
	if row.Agent == agent.NameClaude && configDir != "" {
		hostCmdLineWithEnv = "CLAUDE_CONFIG_DIR=" + shellQuoteSingle(configDir) + " " + hostCmdLine
	}

	switch row.Container {
	case "", "none":
		fmt.Printf("Command: %s\n", hostCmdLineWithEnv)
		if !resumeLaunch {
			return nil
		}
		switch row.Agent {
		case agent.NameClaude:
			return execResume(hostCmd, hostArgs, claudeResumeEnv(configDir))
		case agent.NameCodex, agent.NameCodexTmux:
			return execResume(hostCmd, hostArgs, nil)
		}
		return fmt.Errorf("resume not implemented for agent %q", row.Agent)

	case "docker-exec":
		target := row.ContainerID
		if target == "" {
			target = "<container-name>"
		}
		envFlag := ""
		if row.Agent == agent.NameClaude && configDir != "" {
			envFlag = fmt.Sprintf(" -e CLAUDE_CONFIG_DIR=%s", configDir)
		}
		fmt.Println("Caveat: session lives inside the long-lived container; resuming on the host won't find it.")
		if row.Agent == agent.NameCodex || row.Agent == agent.NameCodexTmux {
			fmt.Println("Caveat: codex auth (OPENAI_API_KEY or ~/.codex/auth.json) must already be set inside the container.")
		}
		fmt.Printf("To try inside the container:\n  docker exec -it%s %s %s\n", envFlag, target, hostCmdLine)
		if resumeLaunch {
			return fmt.Errorf("--launch is not supported for docker-exec runs")
		}
		return nil

	default:
		fmt.Println("Caveat: oneshot container is gone; session state likely unrecoverable.")
		if resumeLaunch {
			return fmt.Errorf("--launch is not supported for %s containers", row.Container)
		}
		return nil
	}
}

// resumeCommand returns the resume command and args for an agent. The
// binary (and optional leading args) comes from ATEAM_RESUME_{CLAUDE,CODEX}_CMD
// when set, otherwise defaults to "claude" / "codex".
//
// codex requires --include-non-interactive because ateam invokes it via
// `codex exec --json`, which the picker hides by default. The same flag
// is harmless for codex-tmux runs (whose rollout also originates from a
// codex CLI session).
func resumeCommand(agentName, sessionID string) (string, []string) {
	switch agentName {
	case agent.NameCodex, agent.NameCodexTmux:
		bin, prefix := resumeBinary(envResumeCodexCmd, "codex")
		args := append(prefix, "resume", "--include-non-interactive", sessionID)
		return bin, args
	default:
		bin, prefix := resumeBinary(envResumeClaudeCmd, "claude")
		args := append(prefix, "--resume", sessionID)
		return bin, args
	}
}

// resumeBinary parses an ATEAM_RESUME_*_CMD env var into (binary, prefixArgs).
// Empty env var falls back to the default name with no prefix args.
func resumeBinary(envKey, def string) (string, []string) {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return def, nil
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return def, nil
	}
	return fields[0], fields[1:]
}

func selectResumeRow(db *calldb.CallDB, args []string) (*calldb.RecentRow, error) {
	if resumeLast {
		// Push the resumable-agents filter to SQL so --last works regardless
		// of how many non-resumable runs (mock, etc.) sit in front.
		rows, err := db.RecentRuns(calldb.RecentFilter{
			Agents: resumableAgents,
			Limit:  1,
		})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("no recent %s runs found", strings.Join(resumableAgents, "/"))
		}
		return &rows[0], nil
	}
	ids, err := parseIDArgs(args)
	if err != nil {
		return nil, err
	}
	rows, err := recentRowsByIDs(db, ids)
	if err != nil {
		return nil, err
	}
	return &rows[0], nil
}

// extractSessionID returns the first non-empty session id found in an agent
// stream JSONL file. Handles both claude (session_id on system init) and
// codex (thread_id on thread.started). Returns "" when no id is present
// (e.g. the run died before init).
//
// Capped at maxSessionScanLines so the inspect-time hint never has to read a
// large stream end-to-end when the init event is missing.
const maxSessionScanLines = 50

func extractSessionID(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 0; n < maxSessionScanLines && scanner.Scan(); n++ {
		line := scanner.Bytes()
		if id := claudeSessionID(line); id != "" {
			return id, nil
		}
		if id := codexThreadID(line); id != "" {
			return id, nil
		}
	}
	return "", scanner.Err()
}

func claudeSessionID(line []byte) string {
	typ, ev, err := streamutil.ParseClaudeLine(line)
	if err != nil || typ != "system" {
		return ""
	}
	sys, ok := ev.(*streamutil.SystemEvent)
	if !ok {
		return ""
	}
	return sys.SessionID
}

func codexThreadID(line []byte) string {
	typ, ev, err := agent.ParseCodexLine(line)
	if err != nil || typ != "system" {
		return ""
	}
	sys, ok := ev.(*agent.CodexSystemEvent)
	if !ok {
		return ""
	}
	return sys.SessionID
}

// cmdMDPath returns the cmd.md path that pairs with a stream file, handling
// both the new layout (logs/<exec_id>/{stream.jsonl, cmd.md}) and the legacy
// prefix layout (<dir>/<TS>_<ACTION>_{stream.jsonl, exec.md}).
func cmdMDPath(streamPath string) string {
	if root.IsLegacyStreamFile(streamPath) {
		return strings.TrimSuffix(streamPath, "_stream.jsonl") + "_exec.md"
	}
	return filepath.Join(filepath.Dir(streamPath), "cmd.md")
}

// resolveResumeConfigDir picks the canonical CLAUDE_CONFIG_DIR for a resume.
// Source priority:
//  1. Value recorded in <prefix>_exec.md under "## Specified" (what was actually used).
//  2. Re-resolved from the row's profile/agent in the current runtime config
//     (best-effort fallback; the definition may have drifted since the run).
//
// Returns ("", "") when neither yields a value.
func resolveResumeConfigDir(execMD string, env *root.ResolvedEnv, row *calldb.RecentRow) (path, source string) {
	if v, ok := readSpecifiedEnv(execMD, "CLAUDE_CONFIG_DIR"); ok {
		return v, "from " + execMD
	}
	if v := configDirFromRuntime(env, row); v != "" {
		return v, "re-resolved from current runtime config; may have drifted"
	}
	return "", ""
}

// readSpecifiedEnv looks up KEY=VALUE under the "## Specified" block of an
// _exec.md file. Open failure is treated as "not found" so the caller can
// transparently fall through to the runtime-config fallback (the file is
// best-effort: older runs predate the field, sandboxed runs may not have it).
func readSpecifiedEnv(execMD, key string) (string, bool) {
	f, err := os.Open(execMD)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inSection := false
	prefix := key + "="
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "## Specified") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(line, "# ") || strings.HasPrefix(line, "## ") {
				return "", false
			}
			if strings.HasPrefix(line, prefix) {
				return strings.TrimPrefix(line, prefix), true
			}
		}
	}
	return "", false
}

func configDirFromRuntime(env *root.ResolvedEnv, row *calldb.RecentRow) string {
	if env == nil || row == nil {
		return ""
	}
	cfg, err := runtime.Load(env.ProjectDir, env.OrgDir)
	if err != nil || cfg == nil {
		return ""
	}
	agentName := row.Agent
	switch {
	case strings.HasPrefix(row.Profile, "a:"):
		// Synthetic profile created by --agent runs (cmd/table.go), where
		// the suffix names the actual agent definition (claude-isolated,
		// claude-sonnet, …). row.Agent only stores the kind ("claude").
		agentName = strings.TrimPrefix(row.Profile, "a:")
	case row.Profile != "":
		if prof, ok := cfg.Profiles[row.Profile]; ok && prof.Agent != "" {
			agentName = prof.Agent
		}
	}
	ac, ok := cfg.Agents[agentName]
	if !ok || ac.ConfigDir == "" {
		return ""
	}
	dir := display.ExpandHome(ac.ConfigDir)
	if !filepath.IsAbs(dir) && env.ProjectDir != "" {
		dir = filepath.Join(env.ProjectDir, dir)
	}
	return dir
}

// execResume replaces the current process with `name args...`, applying
// any extra env overrides (currently just CLAUDE_CONFIG_DIR). Both
// resumable agent kinds (claude / codex / codex-tmux) flow through here.
func execResume(name string, args []string, extraEnv map[string]string) error {
	binary, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%s not found in PATH: %w", name, err)
	}
	envv := os.Environ()
	envPrefix := ""
	for k, v := range extraEnv {
		envv = setEnv(envv, k, v)
		envPrefix += k + "=" + shellQuoteSingle(v) + " "
	}
	argv := append([]string{name}, args...)
	fmt.Println("Interactive resume runs without ateam's sandbox.")
	fmt.Printf("exec %s%s\n", envPrefix, joinCmd(binary, args))
	return syscall.Exec(binary, argv, envv)
}

// claudeResumeEnv returns the env overrides to apply when launching a
// claude resume. Nil/empty configDir means "no override".
func claudeResumeEnv(configDir string) map[string]string {
	if configDir == "" {
		return nil
	}
	return map[string]string{"CLAUDE_CONFIG_DIR": configDir}
}

// joinCmd renders a command + args as a single space-separated line for
// printing. Not shell-safe — display only.
func joinCmd(cmd string, args []string) string {
	if len(args) == 0 {
		return cmd
	}
	return cmd + " " + strings.Join(args, " ")
}
