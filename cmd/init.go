package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
	"github.com/spf13/cobra"
)

var (
	initName        string
	initRoles       []string
	initAutoSetup   bool
	initDebug       bool
	initAgentConfig string
)

var initCmd = &cobra.Command{
	Use:   "init [PATH]",
	Short: "Initialize a project for ATeam",
	Long: `Create a .ateam/ project directory at PATH (defaults to ".") with an
ateam.hcl naming the project, and register it under the user's ateam
directory (~/.ateam, created on first use with a starter ateam.hcl).

The project name is its identity: it must be unique across your projects
and match [a-z0-9._-]. It defaults to the directory name.

Example:
  ateam init
  ateam init --name myproject --role test.gaps,project.security
  ateam init --auto-setup`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initName, "name", "", "project name (defaults to the directory name)")
	initCmd.Flags().StringSliceVar(&initRoles, "role", nil, "roles to enable (if omitted, uses defaults)")
	initCmd.Flags().BoolVar(&initAutoSetup, "auto-setup", false, "run auto-setup after initialization")
	initCmd.Flags().BoolVar(&initDebug, "debug", false, "print step-by-step progress to stderr")
	initCmd.Flags().StringVar(&initAgentConfig, "agent-config", "", "where unattended agents keep their config: home, project, or none (asked on first use; see ateam setup)")
}

func runInit(cmd *cobra.Command, args []string) error {
	dbg := func(msg string) {
		if initDebug {
			fmt.Fprintf(os.Stderr, "[init] %s\n", msg)
		}
	}
	dbg("starting")

	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	dbg("resolving path: " + path)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	absPath = evalSymlinks(absPath)
	dbg("resolved to: " + absPath)

	homeDir := homeFlag
	if homeDir == "" {
		homeDir = runtime.HomeDir()
	}
	dbg("home: " + homeDir)
	_, statErr := os.Stat(filepath.Join(homeDir, root.ConfigFile))
	firstUse := statErr != nil

	// Roles: if --role provided, those are enabled, rest disabled; otherwise use the defaults
	var enabledRoles []string
	if len(initRoles) > 0 {
		dbg("resolving roles...")
		resolved, resolveErr := promptdata.ResolveRoleList(initRoles, nil, []string{homeDir})
		if resolveErr != nil {
			return resolveErr
		}
		enabledRoles = resolved
	}

	dbg("InitProject...")
	_, err = root.InitProject(absPath, homeDir, root.InitProjectOpts{
		Name:         initName,
		EnabledRoles: enabledRoles,
		AllRoles:     promptdata.AllRoleIDs,
	})
	if err != nil {
		return err
	}
	dbg("InitProject done")

	// Re-resolve to show the full env display
	dbg("LookupFrom...")
	env, err := root.LookupFrom(absPath)
	if err != nil {
		return err
	}
	dbg("printEnv...")
	if err := printEnv(env); err != nil {
		return err
	}
	dbg("printEnv done")

	// First use of the user directory, or an explicit choice: offer the
	// separate agent config dir now rather than leaving it to be discovered.
	if firstUse || initAgentConfig != "" {
		fmt.Println()
		if err := setupAgentConfigDirs(env, initAgentConfig, nil, firstUse); err != nil {
			return err
		}
	}

	fmt.Printf("\nNext steps:\n")
	fmt.Printf("\n  1. Edit %s/%s to enable/disable roles and pick a default profile\n", root.DirName, root.ConfigFile)
	fmt.Printf("\n  2. Give unattended agents their own config dir (ateam setup), then log in: ateam login\n")
	fmt.Printf("\n  Main commands:\n")
	fmt.Printf("    ateam env       show current environment and configuration\n")
	fmt.Printf("    ateam report    run role agents to analyze the project\n")
	fmt.Printf("    ateam review    supervisor reviews and prioritizes findings\n")
	fmt.Printf("    ateam code      execute prioritized tasks as code changes\n")
	fmt.Printf("    ateam run-all   run the full pipeline: report + review + code\n")
	fmt.Printf("    ateam ps        show recent agent runs\n")
	fmt.Printf("    ateam tail      live-stream agent output\n")
	fmt.Printf("    ateam prompt    inspect assembled prompts\n")
	fmt.Printf("    ateam serve     browse reports and sessions in a web UI\n")

	if initAutoSetup {
		fmt.Println("\n=== Auto-Setup ===")
		return runAutoSetup(nil, nil)
	}

	return nil
}

func execGitCmd(dir string, gitArgs ...string) string {
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func resolvedCwd() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("cannot get working directory: %w", err)
	}
	return evalSymlinks(cwd), nil
}

func evalSymlinks(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}
