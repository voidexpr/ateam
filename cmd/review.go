package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
	reviewAll             bool
	reviewMaxAge          string
	reviewDockerAutoSetup bool
	reviewContainerName   string
	reviewMaxBudgetUSD    string
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
	Roles           []string      // restrict review to these roles' reports
	IncludeDisabled bool          // include reports from roles disabled in config.toml
	MaxAge          time.Duration // freshness window; zero = no filter
	DockerAutoSetup bool
	ContainerName   string
	MaxBudgetUSD    string
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
		maxAge, err := parseMaxAge(reviewMaxAge)
		if err != nil {
			return err
		}
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
			IncludeDisabled: reviewAll,
			MaxAge:          maxAge,
			DockerAutoSetup: reviewDockerAutoSetup,
			ContainerName:   reviewContainerName,
			MaxBudgetUSD:    reviewMaxBudgetUSD,
		})
	},
}

// parseMaxAge accepts the same set as time.ParseDuration plus "Nd" (treated
// as N*24h). Empty string returns 0 (no filter). Anything mixing days with
// other units is rejected to keep semantics obvious.
func parseMaxAge(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		// Reject "1d2h" — too easy to misread. Stick to plain "Nd".
		body := strings.TrimSuffix(s, "d")
		if body == "" || strings.ContainsAny(body, "dhms") {
			return 0, fmt.Errorf("invalid --max-age %q: use plain Nd, or a stdlib duration like 2h30m", s)
		}
		days, err := strconv.Atoi(body)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid --max-age %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid --max-age %q: %w", s, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid --max-age %q: must be positive", s)
	}
	return d, nil
}

func init() {
	reviewCmd.Flags().StringVar(&reviewExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	reviewCmd.Flags().StringVar(&reviewCustomPrompt, "prompt", "", "custom prompt replacing default supervisor role (text or @filepath)")
	reviewCmd.Flags().IntVar(&reviewTimeout, "timeout", 0, "timeout in minutes (overrides config)")
	reviewCmd.Flags().BoolVar(&reviewPrint, "print", false, "print review to stdout after completion")
	reviewCmd.Flags().BoolVar(&reviewDryRun, "dry-run", false, "print the computed prompt and list reports without running")
	reviewCmd.Flags().StringSliceVar(&reviewRoles, "roles", nil, "limit review to these roles' reports (default: all enabled roles)")
	reviewCmd.Flags().BoolVar(&reviewAll, "all", false, "include reports from roles disabled in config.toml")
	reviewCmd.Flags().StringVar(&reviewMaxAge, "max-age", "", "drop reports older than this (e.g. 2h, 30m, 1d)")
	addCheaperModelFlag(reviewCmd, &reviewCheaperModel)
	addProfileFlags(reviewCmd, &reviewProfile, &reviewAgent)
	addVerboseFlag(reviewCmd, &reviewVerbose)
	addForceFlag(reviewCmd, &reviewForce)
	addDockerAutoSetupFlag(reviewCmd, &reviewDockerAutoSetup)
	addContainerNameFlag(reviewCmd, &reviewContainerName)
	addBudgetFlags(reviewCmd, &reviewMaxBudgetUSD, nil,
		"USD spend cap for the supervisor (claude-only; errors on codex)", "")
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

	selector := prompts.ReviewSelector{
		Roles:           opts.Roles,
		IncludeDisabled: opts.IncludeDisabled,
		MaxAge:          opts.MaxAge,
	}

	pinfo := env.NewProjectInfoParams("the supervisor", "review")
	prompt, err := prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt, customPrompt, selector, env.Config.Roles)
	if err != nil {
		var empty *prompts.ReviewEmptyError
		if errors.As(err, &empty) {
			return formatReviewEmpty(empty.Funnel)
		}
		return err
	}

	timeout := env.Config.Review.EffectiveTimeout(opts.Timeout)

	reviewFile := env.ReviewPath()
	supervisorDir := env.SupervisorDir()

	startedAt := time.Now()

	if opts.DryRun {
		return printReviewDryRun(env, prompt)
	}

	fmt.Printf("Supervisor reviewing reports (%dm timeout)...\n", timeout)

	cr, err := resolveRunner(env, opts.Profile, opts.Agent, runner.ActionReview, "", opts.DockerAutoSetup)
	if err != nil {
		return err
	}
	if err := applyContainerName(cr, env, opts.ContainerName); err != nil {
		return err
	}
	applyCheaperModel(cr, opts.CheaperModel)
	if err := applyMaxBudgetUSD(cr, opts.MaxBudgetUSD, runner.ActionReview); err != nil {
		return err
	}

	db, err := openProjectDB(env)
	if err != nil {
		return err
	}
	defer db.Close()
	cr.CallDB = db

	if !opts.Force {
		if err := checkConcurrentRunsEnv(db, env, runner.ActionReview, nil); err != nil {
			return err
		}
	}

	runOpts := runner.RunOpts{
		RoleID:           "supervisor",
		Action:           runner.ActionReview,
		OutputKind:       runner.OutputKindReview,
		CanonicalDestDir: supervisorDir,
		WorkDir:          env.SourceDir,
		TimeoutMin:       timeout,
		Verbose:          opts.Verbose,
		StartedAt:        startedAt,
	}

	ctx, stop := cmdContext()
	defer stop()
	result := cr.Run(ctx, prompt, runOpts, nil)

	if result.Err != nil {
		return fmt.Errorf("review failed: %w", result.Err)
	}

	// The agent (or runner fallback) already wrote the review into the
	// history dir at OutputFilePath; the runner's success path promoted it
	// to reviewFile. No post-run archive step needed.

	printDone(result)
	fmt.Printf("Review: %s\n", reviewFile)

	if opts.Print {
		fmt.Printf("\n%s\n", result.Output)
	}

	return nil
}

// formatReviewEmpty turns a ReviewFunnel into a stderr-friendly explanation
// of which filters reduced the report set to zero.
func formatReviewEmpty(f prompts.ReviewFunnel) error {
	var b strings.Builder
	b.WriteString("no reports left after filters\n")
	fmt.Fprintf(&b, "  available reports:   %d\n", f.Available)
	if f.HadEnabled {
		fmt.Fprintf(&b, "  enabled roles:       %d\n", f.Enabled)
	}
	if f.HadRoles() {
		fmt.Fprintf(&b, "  matching --roles:    %d  (%s)\n", f.RolesMatch, strings.Join(f.UsedRoles, ", "))
	}
	if f.HadMaxAge() {
		fmt.Fprintf(&b, "  fresher than %s:  %d\n", f.MaxAge, f.FreshEnough)
	}
	var hints []string
	if f.HadMaxAge() {
		hints = append(hints, "widen --max-age")
	}
	if f.HadRoles() {
		hints = append(hints, "drop --roles")
	}
	if f.HadEnabled {
		hints = append(hints, "pass --all to include disabled roles")
	}
	if len(hints) > 0 {
		// Capitalize the first hint's leading rune so the line reads as a sentence.
		first := hints[0]
		b.WriteString(strings.ToUpper(first[:1]) + first[1:])
		for _, h := range hints[1:] {
			b.WriteString(", ")
			b.WriteString(h)
		}
		b.WriteString(".")
	}
	return errors.New(b.String())
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
