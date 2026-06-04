package cmd

import (
	"time"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

// SPEC INVARIANT (Next-round step 6): assembleRoleReport is gone. The
// per-role report prompt composes via NewReportBundle's PromptFile +
// {{dynamic.previous_report}} weaving the prior cycle's report block in.

// ReportBundleInput parameterizes a single role's report bundle. Each
// role gets its own bundle (and its own previous_report dynamic
// closure capturing the role ID), so the dispatcher can resolve N
// roles in parallel without contention.
type ReportBundleInput struct {
	Env                *root.ResolvedEnv
	RoleID             string
	PrePrompt          string
	PostPrompt         string
	SkipPreviousReport bool
	TimeoutMin         int
	Verbose            bool
	Batch              string
	StartedAt          time.Time
}

// previousReportDynamic returns the "# Previous Report" block for roleID
// (or a "no prior report" sentinel for fresh cycles). Mode-agnostic —
// the prior report is a stable input from a previous cycle, like
// project_info, not an artifact this cycle generates.
//
// When skip is true the dynamic returns "" so the dir_post fragment
// collapses (matches assembleRoleReport's legacy skipPreviousReport
// branch byte-for-byte).
func previousReportDynamic(env *root.ResolvedEnv, roleID string, skip bool) prompts.PromptDynamicFunction {
	return func(prompts.ResolveContext, ...string) (string, error) {
		if skip {
			return "", nil
		}
		return previousReportBlock(env, roleID), nil
	}
}

// NewReportBundle constructs the PromptBundle for one role's `ateam
// report` run. Body composes from report/<roleID>.prompt.md anchored
// through dir_pre:report + dir_post:report fragments;
// {{dynamic.previous_report}} appended by the _post.previous_report.md
// fragment weaves the prior cycle's report in.
func NewReportBundle(in ReportBundleInput) *flow.PromptBundle {
	promptPath := "report/" + in.RoleID
	roleLabel := "role " + in.RoleID
	a := in.Env.Assembler()
	vars := in.Env.BuildAssemblerVars(promptPath, roleLabel, "report")
	return &flow.PromptBundle{
		Name:   in.RoleID,
		Role:   in.RoleID,
		Action: runner.ActionReport,
		Prompt: prompts.PromptFile{
			Path:       promptPath,
			PrePrompt:  in.PrePrompt,
			PostPrompt: in.PostPrompt,
			Assembler:  a,
		},
		BaseVars: vars,
		Dynamics: prompts.PromptDynamic{
			"project_info":    in.Env.ProjectInfoDynamic(roleLabel, "report"),
			"previous_report": previousReportDynamic(in.Env, in.RoleID, in.SkipPreviousReport),
		},
		RunOpts: func(flow.RuntimeEnv) runner.RunOpts {
			return runner.RunOpts{
				RoleID:            in.RoleID,
				Action:            runner.ActionReport,
				OutputKind:        runner.OutputKindReport,
				PromptName:        in.RoleID,
				CanonicalDestFile: in.Env.RoleReportPath(in.RoleID),
				WorkDir:           in.Env.WorkDir,
				TimeoutMin:        in.TimeoutMin,
				Verbose:           in.Verbose,
				Batch:             in.Batch,
				StartedAt:         in.StartedAt,
				QuietExecID:       true,
			}
		},
	}
}

// previousReportBlock-related formatting helpers live in report_assemble.go
// (kept around because the dynamic still calls previousReportBlock).
