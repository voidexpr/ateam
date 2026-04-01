package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	reviewExtraPrompt     string
	reviewCustomPrompt    string
	reviewTimeout         int
	reviewPrint           bool
	reviewDryRun          bool
	reviewCheaperModel    bool
	reviewProfile         string
	reviewAgent           string
	reviewVerbose         bool
	reviewForce           bool
	reviewRoles           []string
	reviewDockerAutoSetup bool
)

// ReviewOptions holds configuration for a review run.
type ReviewOptions struct {
	ExtraPrompt     string
	CustomPrompt    string
	Timeout         int
	Print           bool
	DryRun          bool
	CheaperModel    bool
	Profile         string
	Agent           string
	Verbose         bool
	Force           bool
	Roles           []string
	DockerAutoSetup bool
}

var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Supervisor reviews role reports and produces decisions",
	Long: `Read all role reports and have the supervisor produce a prioritized
decisions document.

Works from any project directory — discovers the .ateamorg/ and .ateam/ structure.

Example:
  ateam review
  ateam review --extra-prompt "Focus on security findings"
  ateam review --prompt @custom_review.md`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReview(ReviewOptions{
			ExtraPrompt:     reviewExtraPrompt,
			CustomPrompt:    reviewCustomPrompt,
			Timeout:         reviewTimeout,
			Print:           reviewPrint,
			DryRun:          reviewDryRun,
			CheaperModel:    reviewCheaperModel,
			Profile:         reviewProfile,
			Agent:           reviewAgent,
			Verbose:         reviewVerbose,
			Force:           reviewForce,
			Roles:           reviewRoles,
			DockerAutoSetup: reviewDockerAutoSetup,
		})
	},
}

func init() {
	reviewCmd.Flags().StringVar(&reviewExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reviewCmd.Flags().StringVar(&reviewCustomPrompt, "prompt", "", "custom prompt replacing default supervisor role (text or @filepath)")
	reviewCmd.Flags().IntVar(&reviewTimeout, "timeout", 0, "timeout in minutes (overrides config)")
	reviewCmd.Flags().BoolVar(&reviewPrint, "print", false, "print review to stdout after completion")
	reviewCmd.Flags().BoolVar(&reviewDryRun, "dry-run", false, "print the computed prompt and list reports without running")
	reviewCmd.Flags().StringSliceVar(&reviewRoles, "roles", nil, "limit coding tasks to these roles (reviews all reports but only assigns code tasks to listed roles)")
	addCheaperModelFlag(reviewCmd, &reviewCheaperModel)
	addProfileFlags(reviewCmd, &reviewProfile, &reviewAgent)
	addVerboseFlag(reviewCmd, &reviewVerbose)
	addForceFlag(reviewCmd, &reviewForce)
	addDockerAutoSetupFlag(reviewCmd, &reviewDockerAutoSetup)
}

func runReview(opts ReviewOptions) error {
	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(opts.ExtraPrompt)
	if err != nil {
		return err
	}

	customPrompt, err := prompts.ResolveOptional(opts.CustomPrompt)
	if err != nil {
		return err
	}

	if len(opts.Roles) > 0 {
		if _, err := prompts.ResolveRoleList(opts.Roles, env.Config.Roles, env.ProjectDir, env.OrgDir); err != nil {
			return err
		}
	}

	pinfo := env.NewProjectInfoParams("the supervisor")
	prompt, err := prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt, customPrompt)
	if err != nil {
		return err
	}

	if len(opts.Roles) > 0 {
		prompt += "\n\n---\n\n# Role Constraint\n\n" +
			"Assign coding tasks only to the following roles: " + strings.Join(opts.Roles, ", ") + ". " +
			"Review and assess all reports, but mark coding tasks for unlisted roles as deferred. " +
			"For the listed roles, include tasks you consider worthwhile even if not strictly urgent."
	}

	if opts.DryRun {
		return printReviewDryRun(env, prompt)
	}

	timeout := env.Config.Review.EffectiveTimeout(opts.Timeout)

	reviewFile := env.ReviewPath()
	reviewDir := filepath.Dir(reviewFile)
	historyDir := env.ReviewHistoryDir()

	if err := os.MkdirAll(historyDir, 0755); err != nil {
		return fmt.Errorf("cannot create review history directory: %w", err)
	}

	fmt.Printf("Supervisor reviewing reports (%dm timeout)...\n", timeout)

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionReview, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	applyCheaperModel(cr, opts.CheaperModel)

	db := openProjectDB(env)
	if db != nil {
		defer db.Close()
		cr.CallDB = db
	}

	if !opts.Force {
		if err := checkConcurrentRuns(db, "", runner.ActionReview, nil); err != nil {
			return err
		}
	}

	runOpts := runner.RunOpts{
		RoleID:               "supervisor",
		Action:               runner.ActionReview,
		LogsDir:              env.SupervisorLogsDir(),
		LastMessageFilePath:  reviewFile,
		ErrorMessageFilePath: filepath.Join(reviewDir, "review_error.md"),
		WorkDir:              env.SourceDir,
		TimeoutMin:           timeout,
		HistoryDir:           historyDir,
		PromptName:           "review_prompt.md",
		Verbose:              opts.Verbose,
	}

	ctx, stop := cmdContext()
	defer stop()
	result := cr.Run(ctx, prompt, runOpts, nil)

	if result.Err != nil {
		return fmt.Errorf("review failed: %w", result.Err)
	}

	if err := runner.ArchiveFile(reviewFile, historyDir, "review.md", result.StartedAt); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not archive review: %v\n", err)
	}

	printDone(result)
	fmt.Printf("Review: %s\n", reviewFile)

	if opts.Print {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}

func printReviewDryRun(env *root.ResolvedEnv, prompt string) error {
	reports, _ := prompts.DiscoverReports(env.ProjectDir)

	sort.Slice(reports, func(i, j int) bool {
		return reports[i].ModTime.After(reports[j].ModTime)
	})

	fmt.Println("Reports found:")
	if len(reports) == 0 {
		fmt.Println("  (none)")
	}
	for _, r := range reports {
		relPath, _ := filepath.Rel(filepath.Dir(env.OrgDir), r.Path)
		if relPath == "" {
			relPath = r.Path
		}
		fmt.Printf("  %s  %-30s %s\n", r.ModTime.Format(runner.TimestampFormat), r.RoleID, relPath)
	}

	fmt.Printf("\n╔══ supervisor ══╗\n\n")
	fmt.Println(prompt)
	fmt.Printf("\n╚══ supervisor ══╝\n")
	return nil
}
