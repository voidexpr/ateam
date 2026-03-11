package cmd

import (
	"bufio"
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
	initOrgCreate string
	initOrgHome   bool
)

var initCmd = &cobra.Command{
	Use:   "init [PATH]",
	Short: "Initialize a project for ATeam",
	Long: `Create a .ateam/ project directory at PATH (defaults to ".").

If no .ateamorg/ is found, you are prompted to create one.
Use --org-home or --org-create to skip the prompt.

Example:
  ateam init
  ateam init --name myproject --agent testing_basic,security
  ateam init --org-home`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initGitRemote, "git-remote", "", "git remote origin URL")
	initCmd.Flags().StringVar(&initName, "name", "", "project name (defaults to relative path from org root)")
	initCmd.Flags().StringSliceVar(&initAgents, "agent", nil, "agents to enable (if omitted, uses defaults)")
	initCmd.Flags().StringVar(&initOrgCreate, "org-create", "", "create .ateamorg/ at PATH if none exists")
	initCmd.Flags().BoolVar(&initOrgHome, "org-home", false, "create .ateamorg/ in $HOME if none exists")
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

	cwd, err := resolvedCwd()
	if err != nil {
		return err
	}

	orgDir, err := root.FindOrg(cwd)
	if err != nil {
		orgDir, err = autoCreateOrg(absPath)
		if err != nil {
			return err
		}
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

	// Agents: if --agent provided, those are enabled, rest disabled; otherwise use defaults
	var enabledAgents []string
	if len(initAgents) > 0 {
		resolved, resolveErr := prompts.ResolveAgentList(initAgents, nil)
		if resolveErr != nil {
			return resolveErr
		}
		enabledAgents = resolved
	} else {
		enabledAgents = prompts.DefaultEnabledAgents()
	}

	opts := root.InitProjectOpts{
		Name:            name,
		GitRepo:         gitRepo,
		GitRemoteOrigin: gitRemote,
		EnabledAgents:   enabledAgents,
		AllAgents:       prompts.AllAgentIDs,
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

	fmt.Printf("     Org: %s (%s)\n", relOrg, tildeHome(orgRoot))
	fmt.Printf(" Project: %s\n", name)
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

// autoCreateOrg handles the --org-create, --org-home flags or interactive prompt.
func autoCreateOrg(initTarget string) (string, error) {
	var createAt string

	switch {
	case initOrgCreate != "":
		createAt = initOrgCreate
	case initOrgHome:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		createAt = home
	default:
		selected, err := promptOrgCreate(initTarget)
		if err != nil {
			return "", err
		}
		createAt = selected
	}

	absCreate, err := filepath.Abs(createAt)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}

	orgDir, err := root.InstallOrg(absCreate)
	if err != nil {
		return "", err
	}

	fmt.Printf("Created %s\n\n", orgDir)
	return orgDir, nil
}

func promptOrgCreate(initTarget string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	parentDir := filepath.Dir(initTarget)
	sameDir := filepath.Clean(homeDir) == filepath.Clean(parentDir)

	fmt.Fprintf(os.Stderr, "No .ateamorg/ found.\nCreate one?\n")
	fmt.Fprintf(os.Stderr, "  1) %s (home directory)\n", homeDir)
	if !sameDir {
		fmt.Fprintf(os.Stderr, "  2) %s\n", parentDir)
	}
	fmt.Fprintf(os.Stderr, "  n) skip\n")
	fmt.Fprintf(os.Stderr, "> ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", fmt.Errorf("no input — run 'ateam install' to create .ateamorg/ manually")
	}
	choice := strings.TrimSpace(scanner.Text())

	switch choice {
	case "1":
		return homeDir, nil
	case "2":
		if sameDir {
			return "", fmt.Errorf("invalid choice %q", choice)
		}
		return parentDir, nil
	case "n", "N", "":
		return "", fmt.Errorf("skipped — run 'ateam install' to create .ateamorg/ manually")
	default:
		return "", fmt.Errorf("invalid choice %q — run 'ateam install' to create .ateamorg/ manually", choice)
	}
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
