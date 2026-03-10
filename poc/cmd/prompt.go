package cmd

import (
	"fmt"

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
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Resolve and print the full prompt for an agent",
	Long: `Perform 3-level prompt resolution (project → org → defaults) for a given
agent and action, then print the assembled prompt to stdout.

Example:
  ateam prompt --agent security --action report
  ateam prompt --agent refactor_small --action code
  ateam prompt --agent security --action report --extra-prompt "Focus on auth"
  ateam prompt --agent security --action report --extra-prompt @notes.md`,
	Args: cobra.NoArgs,
	RunE: runPrompt,
}

func init() {
	promptCmd.Flags().StringVar(&promptAgent, "agent", "", "agent name (required)")
	promptCmd.Flags().StringVar(&promptAction, "action", "", "action type: report or code (required)")
	promptCmd.Flags().StringVar(&promptExtraPrompt, "extra-prompt", "", "additional instructions (text or @filepath)")
	promptCmd.Flags().BoolVar(&promptNoProjectInfo, "no-project-info", false, "omit ateam project context from the prompt")
	promptCmd.Flags().BoolVar(&promptIgnorePreviousReport, "ignore-previous-report", false, "do not include the agent's previous report in the prompt")
	_ = promptCmd.MarkFlagRequired("agent")
	_ = promptCmd.MarkFlagRequired("action")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	if !prompts.IsValidAgent(promptAgent) {
		return fmt.Errorf("unknown agent: %s\nValid agents: %s", promptAgent, prompts.AgentFlagUsage())
	}

	if promptAction != "report" && promptAction != "code" {
		return fmt.Errorf("invalid action %q: must be 'report' or 'code'", promptAction)
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
		pinfo = env.NewProjectInfoParams("agent " + promptAgent)
	}

	var assembled string
	switch promptAction {
	case "report":
		assembled, err = prompts.AssembleAgentPrompt(env.OrgDir, env.ProjectDir, promptAgent, env.SourceDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	case "code":
		assembled, err = prompts.AssembleAgentCodePrompt(env.OrgDir, env.ProjectDir, promptAgent, env.SourceDir, extraPrompt, pinfo, promptIgnorePreviousReport)
	}
	if err != nil {
		return err
	}

	fmt.Print(assembled)
	return nil
}
