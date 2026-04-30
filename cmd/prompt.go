package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	promptRole                 string
	promptAction               string
	promptExtraPrompt          string
	promptNoProjectInfo        bool
	promptIgnorePreviousReport bool
	promptSupervisor           bool
	promptFilesOnly            bool
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Resolve and print the full prompt for a role or supervisor",
	Long: `Perform 3-level prompt resolution (project → org → defaults) for a given
role or supervisor action, then print the assembled prompt to stdout.

Example:
  ateam prompt --role security --action report
  ateam prompt --role refactor_small --action code
  ateam prompt --role security --action report --extra-prompt "Focus on auth"
  ateam prompt --supervisor --action review
  ateam prompt --supervisor --action code
  ateam prompt --role security --action report --files-only`,
	Args: cobra.NoArgs,
	RunE: runPrompt,
}

func init() {
	promptCmd.Flags().StringVar(&promptRole, "role", "", "role name")
	promptCmd.Flags().BoolVar(&promptSupervisor, "supervisor", false, "generate supervisor prompt instead of role prompt")
	promptCmd.Flags().StringVar(&promptAction, "action", "", "action type: report, code, or review (required)")
	promptCmd.Flags().StringVar(&promptExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	promptCmd.Flags().BoolVar(&promptNoProjectInfo, "no-project-info", false, "omit ateam project context from the prompt")
	promptCmd.Flags().BoolVar(&promptIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	promptCmd.Flags().BoolVar(&promptFilesOnly, "files-only", false, "list prompt sources with token estimates instead of printing the prompt")
	promptCmd.MarkFlagsMutuallyExclusive("role", "supervisor")
	_ = promptCmd.MarkFlagRequired("action")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	if promptSupervisor {
		return runPromptSupervisor()
	}
	if promptRole == "" {
		return fmt.Errorf("either --role or --supervisor is required")
	}
	return runPromptRole()
}

func runPromptRole() error {
	if promptAction != runner.ActionReport && promptAction != runner.ActionCode {
		return fmt.Errorf("invalid action %q for role: must be 'report' or 'code'", promptAction)
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	if !prompts.IsValidRole(promptRole, env.Config.Roles, env.ProjectDir, env.OrgDir) {
		return fmt.Errorf("unknown role: %s\nValid roles: %s", promptRole, strings.Join(prompts.AllKnownRoleIDs(env.Config.Roles, env.ProjectDir, env.OrgDir), ", "))
	}

	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return err
	}

	var pinfo prompts.ProjectInfoParams
	if !promptNoProjectInfo {
		pinfo = env.NewProjectInfoParams("role "+promptRole, promptAction)
	}

	var sources []prompts.PromptSource
	switch promptAction {
	case runner.ActionReport:
		sources = prompts.TraceRolePromptSources(env.OrgDir, env.ProjectDir, promptRole, env.SourceDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case runner.ActionCode:
		sources = prompts.TraceRoleCodePromptSources(env.OrgDir, env.ProjectDir, promptRole, env.SourceDir, extraPrompt, pinfo)
	}

	if promptFilesOnly {
		printPromptSources(os.Stdout, sources)
		return nil
	}

	var assembled string
	switch promptAction {
	case runner.ActionReport:
		assembled, err = prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, promptRole, env.SourceDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case runner.ActionCode:
		assembled, err = prompts.AssembleRoleCodePrompt(env.OrgDir, env.ProjectDir, promptRole, env.SourceDir, extraPrompt, pinfo)
	}
	if err != nil {
		return err
	}
	fmt.Println(assembled)
	printPromptSources(os.Stderr, sources)
	return nil
}

func runPromptSupervisor() error {
	if promptAction != runner.ActionReview && promptAction != runner.ActionCode {
		return fmt.Errorf("invalid action %q for supervisor: must be 'review' or 'code'", promptAction)
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return err
	}

	var pinfo prompts.ProjectInfoParams
	if !promptNoProjectInfo {
		pinfo = env.NewProjectInfoParams("the supervisor", promptAction)
	}

	var sources []prompts.PromptSource
	switch promptAction {
	case runner.ActionReview:
		sources = prompts.TraceReviewPromptSources(env.OrgDir, env.ProjectDir, pinfo, extraPrompt)
	case runner.ActionCode:
		sources = prompts.TraceCodeManagementPromptSources(env.OrgDir, env.ProjectDir, pinfo, env.ReviewPath(), extraPrompt)
	}

	if promptFilesOnly {
		printPromptSources(os.Stdout, sources)
		return nil
	}

	var assembled string
	switch promptAction {
	case runner.ActionReview:
		assembled, err = prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt, "")
	case runner.ActionCode:
		reviewContent, readErr := os.ReadFile(env.ReviewPath())
		if readErr != nil {
			return errNoReview(env.ReviewPath())
		}
		assembled, err = prompts.AssembleCodeManagementPrompt(env.OrgDir, env.ProjectDir, env.SourceDir, pinfo, string(reviewContent), "", extraPrompt)
	}
	if err != nil {
		return err
	}
	fmt.Println(assembled)
	printPromptSources(os.Stderr, sources)
	return nil
}

func printPromptSources(out io.Writer, sources []prompts.PromptSource) {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "PATH\tLAST MODIFIED\tEST. TOKENS")
	var totalTokens int
	for _, s := range sources {
		tokens := prompts.EstimateTokens(s.Content)
		totalTokens += tokens
		modified := ""
		if !s.ModTime.IsZero() {
			modified = fmtDateAge(s.ModTime)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.DisplayPath(), modified, display.FmtTokens(int64(tokens)))
	}
	fmt.Fprintf(w, "TOTAL\t\t%s\n", display.FmtTokens(int64(totalTokens)))
	w.Flush()
}
