package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var (
	promptRole                 string
	promptAction               string
	promptExtraPrompt          string
	promptNoProjectInfo        bool
	promptIgnorePreviousReport bool
	promptSupervisor           bool
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
  ateam prompt --supervisor --action code`,
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
	if promptAction != "report" && promptAction != "code" {
		return fmt.Errorf("invalid action %q for role: must be 'report' or 'code'", promptAction)
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	if !prompts.IsValidRole(promptRole, env.Config.Roles) {
		return fmt.Errorf("unknown role: %s\nValid roles: %s", promptRole, strings.Join(prompts.AllKnownRoleIDs(env.Config.Roles), ", "))
	}

	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return err
	}

	var pinfo prompts.ProjectInfoParams
	if !promptNoProjectInfo {
		pinfo = env.NewProjectInfoParams("role " + promptRole)
	}

	var assembled string
	switch promptAction {
	case "report":
		assembled, err = prompts.AssembleRolePrompt(env.OrgDir, env.ProjectDir, promptRole, env.SourceDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case "code":
		assembled, err = prompts.AssembleRoleCodePrompt(env.OrgDir, env.ProjectDir, promptRole, env.SourceDir, extraPrompt, pinfo)
	}
	if err != nil {
		return err
	}

	fmt.Print(assembled)
	return nil
}

func runPromptSupervisor() error {
	if promptAction != "review" && promptAction != "code" {
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
		pinfo = env.NewProjectInfoParams("the supervisor")
	}

	var assembled string
	switch promptAction {
	case "review":
		assembled, err = prompts.AssembleReviewPrompt(env.OrgDir, env.ProjectDir, pinfo, extraPrompt, "")
	case "code":
		reviewContent, readErr := os.ReadFile(env.ReviewPath())
		if readErr != nil {
			return fmt.Errorf("no review found at %s; run 'ateam review' first", env.ReviewPath())
		}
		assembled, err = prompts.AssembleCodeManagementPrompt(env.OrgDir, env.ProjectDir, env.SourceDir, pinfo, string(reviewContent), "", extraPrompt)
	}
	if err != nil {
		return err
	}

	fmt.Print(assembled)
	return nil
}
