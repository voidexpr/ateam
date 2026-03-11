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
	promptAgent                string
	promptAction               string
	promptExtraPrompt          string
	promptNoProjectInfo        bool
	promptIgnorePreviousReport bool
	promptSupervisor           bool
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Resolve and print the full prompt for an agent or supervisor",
	Long: `Perform 3-level prompt resolution (project → org → defaults) for a given
agent or supervisor action, then print the assembled prompt to stdout.

Example:
  ateam prompt --agent security --action report
  ateam prompt --agent refactor_small --action code
  ateam prompt --agent security --action report --extra-prompt "Focus on auth"
  ateam prompt --supervisor --action review
  ateam prompt --supervisor --action code`,
	Args: cobra.NoArgs,
	RunE: runPrompt,
}

func init() {
	promptCmd.Flags().StringVar(&promptAgent, "agent", "", "agent name")
	promptCmd.Flags().BoolVar(&promptSupervisor, "supervisor", false, "generate supervisor prompt instead of agent prompt")
	promptCmd.Flags().StringVar(&promptAction, "action", "", "action type: report, code, or review (required)")
	promptCmd.Flags().StringVar(&promptExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	promptCmd.Flags().BoolVar(&promptNoProjectInfo, "no-project-info", false, "omit ateam project context from the prompt")
	promptCmd.Flags().BoolVar(&promptIgnorePreviousReport, "ignore-previous-report", false, "do not include the agent's previous report in the prompt")
	promptCmd.MarkFlagsMutuallyExclusive("agent", "supervisor")
	_ = promptCmd.MarkFlagRequired("action")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	if promptSupervisor {
		return runPromptSupervisor()
	}
	if promptAgent == "" {
		return fmt.Errorf("either --agent or --supervisor is required")
	}
	return runPromptAgent()
}

func runPromptAgent() error {
	if promptAction != "report" && promptAction != "code" {
		return fmt.Errorf("invalid action %q for agent: must be 'report' or 'code'", promptAction)
	}

	env, err := root.Resolve(orgFlag, projectFlag)
	if err != nil {
		return err
	}

	if !prompts.IsValidAgent(promptAgent, env.Config.Agents) {
		return fmt.Errorf("unknown agent: %s\nValid agents: %s", promptAgent, strings.Join(prompts.AllKnownAgentIDs(env.Config.Agents), ", "))
	}

	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return err
	}

	var pinfo prompts.ProjectInfoParams
	if !promptNoProjectInfo {
		pinfo = env.NewProjectInfoParams("agent " + promptAgent)
	}

	var assembled string
	switch promptAction {
	case "report":
		assembled, err = prompts.AssembleAgentPrompt(env.OrgDir, env.ProjectDir, promptAgent, env.SourceDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case "code":
		assembled, err = prompts.AssembleAgentCodePrompt(env.OrgDir, env.ProjectDir, promptAgent, env.SourceDir, extraPrompt, pinfo)
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
