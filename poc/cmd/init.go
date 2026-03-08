package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var (
	initGitRemote string
	initName      string
	initAgents    []string
)

var initCmd = &cobra.Command{
	Use:   "init [PATH]",
	Short: "Initialize a project for ATeam",
	Long: `Create a .ateam/ project directory at PATH (defaults to ".").

Requires a .ateamorg/ discoverable from the current directory.

Example:
  ateam init
  ateam init --name myproject --agent testing_basic,security`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initGitRemote, "git-remote", "", "git remote origin URL")
	initCmd.Flags().StringVar(&initName, "name", "", "project name (defaults to relative path from org root)")
	initCmd.Flags().StringSliceVar(&initAgents, "agent", nil, "agents to enable (if omitted, all are enabled)")
}

func runInit(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}
	absPath = evalSymlinks(absPath)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("cannot get working directory: %w", err)
	}
	cwd = evalSymlinks(cwd)

	orgDir, err := root.FindOrg(cwd)
	if err != nil {
		return err
	}
	orgRoot := filepath.Dir(orgDir)

	// Name: relative path from org root to project dir
	name := initName
	if name == "" {
		rel, relErr := filepath.Rel(orgRoot, absPath)
		if relErr != nil {
			rel = filepath.Base(absPath)
		}
		name = rel
	}

	// Git: auto-discover from project dir
	gitRepo := ""
	gitRemote := initGitRemote

	gitTopLevel := execGitCmd(absPath, "rev-parse", "--show-toplevel")
	if gitTopLevel != "" {
		rel, relErr := filepath.Rel(absPath, gitTopLevel)
		if relErr == nil {
			gitRepo = rel
		} else {
			gitRepo = gitTopLevel
		}
	}

	if gitRemote == "" {
		gitRemote = execGitCmd(absPath, "config", "remote.origin.url")
	}

	// Agents: if --agent provided, those are enabled, rest disabled; if not, all enabled
	allAgentIDs := prompts.AllAgentIDs
	var enabledAgents []string
	if len(initAgents) > 0 {
		resolved, resolveErr := prompts.ResolveAgentList(initAgents)
		if resolveErr != nil {
			return resolveErr
		}
		enabledAgents = resolved
	} else {
		enabledAgents = allAgentIDs
	}

	opts := root.InitProjectOpts{
		Name:            name,
		GitRepo:         gitRepo,
		GitRemoteOrigin: gitRemote,
		EnabledAgents:   enabledAgents,
		AllAgents:       allAgentIDs,
	}

	_, err = root.InitProject(absPath, orgDir, opts)
	if err != nil {
		return err
	}

	// Display
	relOrg, _ := filepath.Rel(cwd, orgRoot)
	displayGit := ""
	if gitTopLevel != "" {
		displayGit, _ = filepath.Rel(orgRoot, gitTopLevel)
	}

	fmt.Printf("     Org: %s\n", relOrg)
	fmt.Printf("    Name: %s\n", name)
	if displayGit != "" {
		fmt.Printf("     Git: %s\n", displayGit)
	}
	if gitRemote != "" {
		fmt.Printf("  Remote: %s\n", gitRemote)
	}
	fmt.Printf("  Agents: %s\n", strings.Join(enabledAgents, ", "))
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  ateam report --agents all\n")
	fmt.Printf("  ateam review\n")

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

func evalSymlinks(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}
