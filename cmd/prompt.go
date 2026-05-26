package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
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
	promptPreview              bool
	promptPreviewContent       bool
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
  ateam prompt --supervisor --action verify
  ateam prompt --role security --action report --files-only`,
	Args: cobra.NoArgs,
	RunE: runPrompt,
}

func init() {
	promptCmd.Flags().StringVar(&promptRole, "role", "", "role name")
	promptCmd.Flags().BoolVar(&promptSupervisor, "supervisor", false, "generate supervisor prompt instead of role prompt")
	promptCmd.Flags().StringVar(&promptAction, "action", "", "action type: report, code, review, or verify (required)")
	promptCmd.Flags().StringVar(&promptExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	promptCmd.Flags().BoolVar(&promptNoProjectInfo, "no-project-info", false, "omit ateam project context from the prompt")
	promptCmd.Flags().BoolVar(&promptIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	promptCmd.Flags().BoolVar(&promptFilesOnly, "files-only", false, "list prompt sources with token estimates instead of printing the prompt")
	promptCmd.Flags().BoolVar(&promptPreview, "preview", false, "assemble via the v1 assembler and print a per-section breakdown (anchor + path + slot)")
	promptCmd.Flags().BoolVar(&promptPreviewContent, "content", false, "with --preview, also print the full assembled text")
	promptCmd.MarkFlagsMutuallyExclusive("role", "supervisor")
	_ = promptCmd.MarkFlagRequired("action")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	if promptPreview {
		return runPromptPreview()
	}
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

	env, err := resolveEnv()
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
		sources = prompts.TraceRolePromptSources(env.OrgDir, env.ProjectDir, promptRole, env.WorkDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case runner.ActionCode:
		sources = prompts.TraceRoleCodePromptSources(env.OrgDir, env.ProjectDir, promptRole, env.WorkDir, extraPrompt, pinfo)
	}

	if promptFilesOnly {
		printPromptSources(os.Stdout, sources)
		return nil
	}

	var assembled string
	switch promptAction {
	case runner.ActionReport:
		assembled, err = prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, promptRole, env.WorkDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case runner.ActionCode:
		assembled, err = prompts.AssembleRoleCodePrompt(env.OrgDir, env.ProjectDir, promptRole, env.WorkDir, extraPrompt, pinfo)
	}
	if err != nil {
		return err
	}
	fmt.Println(assembled)
	printPromptSources(os.Stderr, sources)
	return nil
}

func runPromptSupervisor() error {
	if promptAction != runner.ActionReview && promptAction != runner.ActionCode && promptAction != runner.ActionVerify {
		return fmt.Errorf("invalid action %q for supervisor: must be 'review', 'code', or 'verify'", promptAction)
	}

	env, err := resolveEnv()
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
	case runner.ActionVerify:
		sources = prompts.TraceCodeVerifyPromptSources(env.OrgDir, env.ProjectDir, pinfo, extraPrompt)
	}

	if promptFilesOnly {
		printPromptSources(os.Stdout, sources)
		return nil
	}

	var assembled string
	switch promptAction {
	case runner.ActionReview:
		assembled, err = prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt, "", prompts.ReviewSelector{}, env.Config.Roles)
	case runner.ActionCode:
		reviewContent, readErr := os.ReadFile(env.ReviewPath())
		if readErr != nil {
			return errNoReview(env.ReviewPath())
		}
		assembled, err = prompts.AssembleCodeManagementPrompt(env.OrgDir, env.ProjectDir, env.WorkDir, pinfo, string(reviewContent), "", extraPrompt)
	case runner.ActionVerify:
		assembled, err = prompts.AssembleCodeVerifyPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt)
	}
	if err != nil {
		return err
	}
	fmt.Println(assembled)
	printPromptSources(os.Stderr, sources)
	return nil
}

// runPromptPreview uses the v1 assembler to compose the prompt for the given
// role + action (or supervisor + action) and prints a per-section breakdown
// showing which anchor + path + slot each fragment came from. With --content
// it also prints the joined prompt text. Errors loudly on missing roles,
// orphan fragments, and template render failures — the spec's stated goals
// for the preview command.
func runPromptPreview() error {
	env, err := resolveEnv()
	if err != nil {
		return err
	}
	promptPath, roleLabel, err := promptPathForCurrentFlags()
	if err != nil {
		return err
	}
	a := env.Assembler()

	// Surface orphans before assembly so a typo is caught first.
	orphans, _ := a.FindOrphans()
	for _, o := range orphans {
		fmt.Fprintln(os.Stderr, o.Error())
	}

	vars := env.BuildAssemblerVars(promptPath, roleLabel, promptAction)
	res, err := a.Assemble(promptPath, vars, nil)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Assembly for %q (%d sections)\n\n", promptPath, len(res.Sections))
	fmt.Fprintln(w, "SLOT\tANCHOR\tPATH")
	for _, s := range res.Sections {
		fmt.Fprintf(w, "%s\t[%s]\t%s\n", s.Slot, s.Anchor, s.Path)
	}
	w.Flush()

	if promptPreviewContent {
		fmt.Println()
		fmt.Println("--- assembled prompt ---")
		fmt.Println(res.Prompt)
	}
	return nil
}

// promptPathForCurrentFlags maps the existing --role/--supervisor/--action
// flag combo to the v1 promptPath (e.g. "report/security", "review") plus a
// roleLabel for the project info block. The mapping intentionally mirrors
// the old runPromptRole / runPromptSupervisor branches so preview output
// matches what `ateam report` etc. would actually assemble.
func promptPathForCurrentFlags() (path, label string, err error) {
	if promptSupervisor {
		switch promptAction {
		case runner.ActionReview:
			return "review", "the supervisor", nil
		case runner.ActionCode:
			return "code_management", "the supervisor", nil
		case runner.ActionVerify:
			return "code_verify", "the supervisor", nil
		}
		return "", "", fmt.Errorf("invalid action %q for supervisor: must be 'review', 'code', or 'verify'", promptAction)
	}
	if promptRole == "" {
		return "", "", fmt.Errorf("either --role or --supervisor is required")
	}
	switch promptAction {
	case runner.ActionReport:
		return "report/" + promptRole, "role " + promptRole, nil
	case runner.ActionCode:
		return "code/" + promptRole, "role " + promptRole, nil
	}
	return "", "", fmt.Errorf("invalid action %q for role: must be 'report' or 'code'", promptAction)
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
			modified = display.FmtDateAge(s.ModTime)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.DisplayPath(), modified, display.FmtTokens(int64(tokens)))
	}
	fmt.Fprintf(w, "TOTAL\t\t%s\n", display.FmtTokens(int64(totalTokens)))
	w.Flush()
}
