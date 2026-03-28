package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/spf13/cobra"
)

var (
	initGitRemote       string
	initName            string
	initRoles           []string
	initOrgCreate       string
	initOrgHome         bool
	initAutoSetup       bool
	initOrgCreatePrompt bool
)

var initCmd = &cobra.Command{
	Use:   "init [PATH]",
	Short: "Initialize a project for ATeam",
	Long: `Create a .ateam/ project directory at PATH (defaults to ".").

If no .ateamorg/ is found, one is created in $HOME by default.
Use --org-create-prompt for an interactive choice, or --org-create PATH.

Example:
  ateam init
  ateam init --name myproject --role testing_basic,security
  ateam init --org-home`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initGitRemote, "git-remote", "", "git remote origin URL")
	initCmd.Flags().StringVar(&initName, "name", "", "project name (defaults to relative path from org root)")
	initCmd.Flags().StringSliceVar(&initRoles, "role", nil, "roles to enable (if omitted, uses defaults)")
	initCmd.Flags().StringVar(&initOrgCreate, "org-create", "", "create .ateamorg/ at PATH if none exists")
	initCmd.Flags().BoolVar(&initOrgHome, "org-home", false, "create .ateamorg/ in $HOME if none exists")
	initCmd.Flags().BoolVar(&initAutoSetup, "auto-setup", false, "run auto-setup after initialization")
	initCmd.Flags().BoolVar(&initOrgCreatePrompt, "org-create-prompt", false, "interactively choose where to create .ateamorg/")
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

	// Roles: if --role provided, those are enabled, rest disabled; otherwise use template defaults
	var enabledRoles []string
	if len(initRoles) > 0 {
		resolved, resolveErr := prompts.ResolveRoleList(initRoles, nil)
		if resolveErr != nil {
			return resolveErr
		}
		enabledRoles = resolved
	}

	opts := root.InitProjectOpts{
		Name:            name,
		GitRepo:         gitRepo,
		GitRemoteOrigin: gitRemote,
		EnabledRoles:    enabledRoles,
		AllRoles:        prompts.AllRoleIDs,
	}

	_, err = root.InitProject(absPath, orgDir, opts)
	if err != nil {
		return err
	}

	// Re-resolve to show the full env display
	env, err := root.Lookup()
	if err != nil {
		return err
	}
	if err := printEnv(env); err != nil {
		return err
	}

	fmt.Printf("\nNext steps:\n")
	fmt.Printf("\n  1. Edit .ateam/config.toml to enable/disable specific roles as needed\n")
	fmt.Printf("\n  2. Set up authentication:\n")
	fmt.Printf("       ateam secret ANTHROPIC_API_KEY\n")
	fmt.Printf("\n  3. Main commands:\n")
	fmt.Printf("    ateam env       show current environment and configuration\n")
	fmt.Printf("    ateam report    run role agents to analyze the project\n")
	fmt.Printf("    ateam review    supervisor reviews and prioritizes findings\n")
	fmt.Printf("    ateam code      execute prioritized tasks as code changes\n")
	fmt.Printf("    ateam all       run the full pipeline: report + review + code\n")
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
	case initOrgCreatePrompt:
		selected, err := promptOrgCreate(initTarget)
		if err != nil {
			return "", err
		}
		createAt = selected
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		createAt = home
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
	fmt.Fprintf(os.Stderr, "  1) %s (home directory) [default]\n", homeDir)
	if !sameDir {
		fmt.Fprintf(os.Stderr, "  2) %s\n", parentDir)
	}
	fmt.Fprintf(os.Stderr, "  n) cancel\n")
	fmt.Fprintf(os.Stderr, "> ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", fmt.Errorf("no input — run 'ateam install' to create .ateamorg/ manually")
	}
	choice := strings.TrimSpace(scanner.Text())

	switch choice {
	case "1", "":
		return homeDir, nil
	case "2":
		if sameDir {
			return "", fmt.Errorf("invalid choice %q", choice)
		}
		return parentDir, nil
	case "n", "N":
		return "", fmt.Errorf("cancelled")
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
