package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
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
	Short: "Resume an interactive claude session from a previous agent run",
	Long: `Look up the claude session id from a previous run's stream file and either
print the resume command or launch claude --resume directly.

The resumed session is interactive and runs outside ateam: it gets no
agent_execs row, no sandbox settings, and no tracking. It picks up from
the original run's last turn.

Resume is supported for runs whose agent is "claude" and whose container
is "none". For docker / docker-exec runs the session id is still printed
but --launch is refused since the session state lives inside (or with) the
container, not on the host.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runResume,
}

func init() {
	resumeCmd.Flags().BoolVar(&resumeLast, "last", false, "resume the most recent claude run")
	resumeCmd.Flags().BoolVar(&resumeLaunch, "launch", false, "exec into the resume command instead of just printing it")
	rootCmd.AddCommand(resumeCmd)
}

func runResume(cmd *cobra.Command, args []string) error {
	if len(args) == 0 && !resumeLast {
		return fmt.Errorf("specify an exec ID or --last")
	}
	if len(args) > 0 && resumeLast {
		return fmt.Errorf("--last cannot be combined with an exec ID")
	}

	env, err := root.Resolve(orgFlag, projectFlag)
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
	if row.Agent != "claude" {
		return fmt.Errorf("resume only supports claude (run %d used agent %q)", row.ID, row.Agent)
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

	switch row.Container {
	case "", "none":
		cmdLine := fmt.Sprintf("claude --resume %s", sessionID)
		fmt.Printf("Command: %s\n", cmdLine)
		if !resumeLaunch {
			return nil
		}
		return execClaudeResume(sessionID, configDir)

	case "docker-exec":
		target := row.ContainerID
		if target == "" {
			target = "<container-name>"
		}
		envFlag := ""
		if configDir != "" {
			envFlag = fmt.Sprintf(" -e CLAUDE_CONFIG_DIR=%s", configDir)
		}
		fmt.Println("Caveat: session lives inside the long-lived container; resuming on the host won't find it.")
		fmt.Printf("To try inside the container:\n  docker exec -it%s %s claude --resume %s\n", envFlag, target, sessionID)
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

func selectResumeRow(db *calldb.CallDB, args []string) (*calldb.RecentRow, error) {
	if resumeLast {
		rows, err := db.RecentRuns(calldb.RecentFilter{Agent: "claude", Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("no recent claude runs found")
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

// extractSessionID returns the first non-empty session_id found in a Claude
// stream JSONL file. Returns "" when the file has no system event with a
// session_id (e.g. the run died before init).
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
		typ, ev, perr := streamutil.ParseClaudeLine(scanner.Bytes())
		if perr != nil || typ != "system" {
			continue
		}
		sys, ok := ev.(*streamutil.SystemEvent)
		if !ok {
			continue
		}
		if sys.SessionID != "" {
			return sys.SessionID, nil
		}
	}
	return "", scanner.Err()
}

// cmdMDPath returns the cmd.md path that pairs with a stream file, handling
// both the new layout (logs/<exec_id>/{stream.jsonl, cmd.md}) and the legacy
// prefix layout (<dir>/<TS>_<ACTION>_{stream.jsonl, exec.md}).
func cmdMDPath(streamPath string) string {
	if strings.HasSuffix(streamPath, "_stream.jsonl") {
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
	dir := runner.ExpandHome(ac.ConfigDir)
	if !filepath.IsAbs(dir) && env.ProjectDir != "" {
		dir = filepath.Join(env.ProjectDir, dir)
	}
	return dir
}

func execClaudeResume(sessionID, configDir string) error {
	binary, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}
	envv := os.Environ()
	if configDir != "" {
		envv = setEnv(envv, "CLAUDE_CONFIG_DIR", configDir)
	}
	fmt.Println("Interactive resume runs without ateam's sandbox.")
	fmt.Printf("exec %s --resume %s\n", binary, sessionID)
	return syscall.Exec(binary, []string{"claude", "--resume", sessionID}, envv)
}
