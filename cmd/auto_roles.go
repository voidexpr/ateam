package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

// addAutoRolesFlags wires --auto-roles and --plan-only onto a cobra command.
// Centralized so report.go and all.go share identical flag definitions and
// help text.
func addAutoRolesFlags(cmd *cobra.Command, autoRolesDst, planOnlyDst *bool) {
	cmd.Flags().BoolVar(autoRolesDst, "auto-roles", false, "spawn a planner agent to pick which roles to run based on git history and prior runs")
	cmd.Flags().BoolVar(planOnlyDst, "plan-only", false, "with --auto-roles: print the recommendation and exit before running anything")
}

// runAutoRoles invokes the planner agent, prints its rationale, and decides
// whether the caller should proceed. Returns done=true when the caller should
// `return nil` (planner recommended no roles, or PlanOnly was requested). The
// role list is meaningful only when done is false.
func runAutoRoles(env *root.ResolvedEnv, profile, agentName string, verbose, planOnly, dockerAutoSetup bool) (roles []string, done bool, err error) {
	rationale, recommended, err := autoRolesRecommend(env, profile, agentName, verbose, dockerAutoSetup)
	if err != nil {
		return nil, false, err
	}
	if rationale != "" {
		fmt.Println(rationale)
		fmt.Println()
	}
	if len(recommended) == 0 {
		fmt.Println("Auto-roles: no roles recommended for this round.")
		return nil, true, nil
	}
	if planOnly {
		return nil, true, nil
	}
	fmt.Printf("Auto-roles: running %s\n\n", strings.Join(recommended, ","))
	return recommended, false, nil
}

// autoRolesRecommend spawns the supervisor recommendation agent. Returns the
// rationale (markdown text, suitable to print verbatim), the validated role
// list, and any error. An empty role list with a nil error means the agent
// recommended no work.
//
// Profile/agent override the runner selection the same way they do for the
// surrounding `report` / `all` invocation.
func autoRolesRecommend(env *root.ResolvedEnv, profile, agentName string, verbose, dockerAutoSetup bool) (rationale string, roles []string, err error) {
	commandsOutput, err := buildAutoRolesContext(env)
	if err != nil {
		return "", nil, fmt.Errorf("build auto-roles context: %w", err)
	}

	pinfo := env.NewProjectInfoParams("the supervisor", "auto-roles")
	prompt, err := prompts.AssembleAutoRolesPrompt(env.OrgDir, env.ProjectDir, pinfo)
	if err != nil {
		return "", nil, fmt.Errorf("assemble auto-roles prompt: %w", err)
	}

	cr, err := resolveRunner(env, profile, agentName, runner.ActionReview, "", dockerAutoSetup)
	if err != nil {
		return "", nil, fmt.Errorf("resolve auto-roles runner: %w", err)
	}
	setSourceWritable(cr)

	db, err := openProjectDB(env)
	if err != nil {
		return "", nil, fmt.Errorf("open project DB for auto-roles: %w", err)
	}
	defer db.Close()
	cr.CallDB = db

	timeout := env.Config.Review.EffectiveTimeout(0)

	progress := make(chan runner.RunProgress, 64)
	var progressWg sync.WaitGroup
	progressWg.Add(1)
	go func() {
		defer progressWg.Done()
		printProgress(progress)
	}()

	ctx, stop := cmdContext()
	defer stop()

	summary := cr.Run(ctx, prompt, runner.RunOpts{
		RoleID:                  "supervisor",
		Action:                  runner.ActionExec,
		OutputKind:              runner.OutputKindAutoRoles,
		WorkDir:                 env.WorkDir,
		TimeoutMin:              timeout,
		Verbose:                 verbose,
		AutoRolesCommandsOutput: commandsOutput,
	}, progress)

	close(progress)
	progressWg.Wait()

	if summary.Err != nil {
		return "", nil, fmt.Errorf("auto-roles agent failed: %w", summary.Err)
	}
	if summary.ExecID == 0 {
		return "", nil, fmt.Errorf("auto-roles agent returned no exec ID; cannot locate output file")
	}

	outputPath := filepath.Join(env.RuntimeDir(summary.ExecID), runner.PrimaryOutputName(runner.OutputKindAutoRoles))
	content, err := os.ReadFile(outputPath)
	if err != nil {
		return "", nil, fmt.Errorf("read auto-roles output (%s): %w", outputPath, err)
	}

	rationale, roles, err = parseAutoRolesOutput(string(content))
	if err != nil {
		return "", nil, fmt.Errorf("parse auto-roles output (%s): %w", outputPath, err)
	}

	// Validate every recommended role exists. Unknown role = hard error so the
	// user catches a misfiring planner instead of silently running nothing.
	for _, name := range roles {
		if !prompts.IsValidRole(name, env.Config.Roles, env.ProjectDir, env.OrgDir) {
			return rationale, nil, fmt.Errorf("auto-roles recommended unknown role %q (see %s)", name, outputPath)
		}
	}

	return rationale, roles, nil
}

// parseAutoRolesOutput splits the agent's output file into the rationale
// (everything above the marker line) and the recommended role list (parsed
// from the last `RECOMMENDED_ROLES:` marker line).
//
// Returns an error if no marker line is found. An empty role list (the agent
// wrote `RECOMMENDED_ROLES:` with nothing after) is valid and returns
// (rationale, nil, nil).
func parseAutoRolesOutput(content string) (rationale string, roles []string, err error) {
	lines := strings.Split(content, "\n")
	markerIdx := -1
	var markerValue string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, prompts.AutoRolesMarker); ok {
			markerIdx = i
			markerValue = strings.TrimSpace(rest)
		}
	}
	if markerIdx < 0 {
		return "", nil, fmt.Errorf("missing %q marker line", prompts.AutoRolesMarker)
	}

	rationale = strings.TrimRight(strings.Join(lines[:markerIdx], "\n"), "\n")

	if markerValue == "" {
		return rationale, nil, nil
	}
	for _, part := range strings.Split(markerValue, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			roles = append(roles, name)
		}
	}
	return rationale, roles, nil
}
