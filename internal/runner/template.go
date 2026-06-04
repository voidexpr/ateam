package runner

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
)

// TemplateVars holds the variables available for {{VAR}} substitution
// in agent args and extra args from runtime.hcl.
type TemplateVars struct {
	ProjectName       string // from config.toml project.name
	ProjectFullPath   string // absolute path to project source dir
	ProjectDir        string // last component of ProjectFullPath
	Role              string // role ID (e.g. "security", "supervisor")
	Action            string // action type (e.g. "report", "exec", "code")
	Batch             string // batch ID (e.g. "code-2026-03-31_06-09-39")
	Timestamp         string // run start time (TimestampFormat)
	Profile           string // active profile name
	ExecID            int64  // call tracking ID (from ateam ps)
	Agent             string // agent config name (e.g. "claude", "claude-docker")
	Model             string // resolved model name
	Effort            string // reasoning effort (e.g. "low", "high"); empty if unset
	MaxBudgetUSD      string // per-exec USD spend cap; empty if unset
	MaxBudgetUSDBatch string // batch-wide USD spend cap; empty if unset
	SubRunArgs        string // opaque CLI args fragment; surfaced as {{SUBRUN_ARGS}}
	ContainerType     string // container type ("none", "docker", "docker-exec", etc.)
	ContainerName     string // docker container name (e.g. "ateam-myapp-security")
	OutputDir         string // absolute path to <projectDir>/runtime/<exec_id>/ (where the agent should write files)
	OutputFile        string // absolute path to OutputDir/<primary_kind> (e.g. report.md); empty when the action has no primary output
	PromptFile        string // absolute path to the archived prompt (logs/<exec_id>/prompt.md); set after Prepare allocates the logs dir

	// AutoRolesCommandsOutput is the pre-baked context bundle injected into
	// `{{ATEAM_AUTO_ROLES_COMMANDS_OUTPUT}}` for the --auto-roles planner agent.
	// Copied from RunOpts.AutoRolesCommandsOutput; empty for every other action.
	AutoRolesCommandsOutput string
}

// Replacer builds a strings.Replacer for the current template vars.
// Call once per Run() and reuse across all resolution calls.
//
// Each runner var is registered under both its legacy ALL_CAPS form and
// the dotted form (`{{exec.id}}`, `{{container.name}}`, etc.). Defaults
// ship the dotted form as the canonical surface — the ALL_CAPS aliases
// stay for back-compat with hand-authored prompts and runtime.hcl files
// that pre-date the lifecycle refactor. ALL_CAPS aliases are slated for
// removal once the deprecation window closes (see Step 6's allcaps
// guardrail in internal/runtime/allcaps_check.go).
func (v TemplateVars) Replacer() *strings.Replacer {
	execID := ""
	if v.ExecID > 0 {
		execID = fmt.Sprintf("%d", v.ExecID)
	}
	return strings.NewReplacer(
		// project.*
		"{{project.name}}", v.ProjectName, "{{PROJECT_NAME}}", v.ProjectName,
		"{{project.full_path}}", v.ProjectFullPath, "{{PROJECT_FULL_PATH}}", v.ProjectFullPath,
		"{{project.dir}}", v.ProjectDir, "{{PROJECT_DIR}}", v.ProjectDir,
		// prompt.* (role/action are prompt-scoped identity)
		"{{prompt.name}}", v.Role, "{{ROLE}}", v.Role,
		"{{prompt.action}}", v.Action, "{{ACTION}}", v.Action,
		// exec.*
		"{{exec.batch}}", v.Batch, "{{BATCH}}", v.Batch,
		"{{exec.timestamp}}", v.Timestamp, "{{TIMESTAMP}}", v.Timestamp,
		"{{exec.profile}}", v.Profile, "{{PROFILE}}", v.Profile,
		"{{exec.id}}", execID, "{{EXEC_ID}}", execID,
		"{{exec.agent}}", v.Agent, "{{AGENT}}", v.Agent,
		"{{exec.model}}", v.Model, "{{MODEL}}", v.Model,
		"{{exec.effort}}", v.Effort, "{{EFFORT}}", v.Effort,
		"{{exec.max_budget_usd}}", v.MaxBudgetUSD, "{{MAX_BUDGET_USD}}", v.MaxBudgetUSD,
		"{{exec.max_budget_usd_batch}}", v.MaxBudgetUSDBatch, "{{MAX_BUDGET_USD_BATCH}}", v.MaxBudgetUSDBatch,
		"{{exec.subrun_args}}", v.SubRunArgs, "{{SUBRUN_ARGS}}", v.SubRunArgs,
		"{{exec.output_dir}}", v.OutputDir, "{{OUTPUT_DIR}}", v.OutputDir,
		"{{exec.output_file}}", v.OutputFile, "{{OUTPUT_FILE}}", v.OutputFile,
		"{{exec.prompt_file}}", v.PromptFile,
		"{{exec.auto_roles_commands_output}}", v.AutoRolesCommandsOutput, "{{ATEAM_AUTO_ROLES_COMMANDS_OUTPUT}}", v.AutoRolesCommandsOutput,
		// Legacy alias for code_management_prompt.md; same dir as OUTPUT_DIR.
		"{{EXECUTION_DIR}}", v.OutputDir,
		// container.*
		"{{container.type}}", v.ContainerType, "{{CONTAINER_TYPE}}", v.ContainerType,
		"{{container.name}}", v.ContainerName, "{{CONTAINER_NAME}}", v.ContainerName,
		// ateam.* — ateam's own embedded docs.
		"{{ateam.own_readme}}", defaults.SelfDocs["README"], "{{ATEAM_OWN_README}}", defaults.SelfDocs["README"],
		"{{ateam.own_commands}}", defaults.SelfDocs["COMMANDS"], "{{ATEAM_OWN_COMMANDS}}", defaults.SelfDocs["COMMANDS"],
		"{{ateam.own_config}}", defaults.SelfDocs["CONFIG"], "{{ATEAM_OWN_CONFIG}}", defaults.SelfDocs["CONFIG"],
		"{{ateam.own_isolation}}", defaults.SelfDocs["ISOLATION"], "{{ATEAM_OWN_ISOLATION}}", defaults.SelfDocs["ISOLATION"],
		"{{ateam.own_roles}}", defaults.SelfDocs["ROLES"], "{{ATEAM_OWN_ROLES}}", defaults.SelfDocs["ROLES"],
		"{{ateam.auto_roles_marker}}", prompts.AutoRolesMarker, "{{AUTO_ROLES_MARKER}}", prompts.AutoRolesMarker,
	)
}

// ResolveTemplateArgs replaces {{VAR}} placeholders in each arg string.
// Unknown variables are left as-is.
func ResolveTemplateArgs(args []string, vars TemplateVars) []string {
	return resolveArgs(args, vars.Replacer())
}

func resolveArgs(args []string, r *strings.Replacer) []string {
	resolved := make([]string, len(args))
	for i, arg := range args {
		resolved[i] = r.Replace(arg)
	}
	return resolved
}

// ResolveAgentTemplateArgs resolves templates in the agent's Args, Env values,
// and other string fields. Clones the agent so the original config is never mutated.
func ResolveAgentTemplateArgs(a agent.Agent, vars TemplateVars) agent.Agent {
	return a.CloneWithResolvedTemplates(vars.Replacer())
}

// ResolveTemplateString replaces {{VAR}} placeholders in a single string.
func ResolveTemplateString(s string, vars TemplateVars) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return vars.Replacer().Replace(s)
}

// ResolveTemplateMap replaces {{VAR}} placeholders in map values.
// Keys are not resolved. Returns a new map.
func ResolveTemplateMap(m map[string]string, vars TemplateVars) map[string]string {
	return resolveMap(m, vars.Replacer())
}

func resolveMap(m map[string]string, r *strings.Replacer) map[string]string {
	if m == nil {
		return nil
	}
	resolved := make(map[string]string, len(m))
	for k, v := range m {
		resolved[k] = r.Replace(v)
	}
	return resolved
}

// resolveContainerTemplates resolves {{VAR}} placeholders in container config fields.
// Mutates the container in place. Each container type handles its own fields via
// the ResolveTemplates method on the Container interface.
func resolveContainerTemplates(c container.Container, vars TemplateVars) {
	if c == nil {
		return
	}
	c.ResolveTemplates(vars.Replacer())
}

// TemplateVarsFor is the convenience entry point: derives agentName/model
// from r.Agent and delegates to BuildTemplateVars. Use this from dry-run /
// preview paths so the substitution they print matches what Run() would
// produce — single source of truth for which {{VARS}} resolve and how.
// callID == 0 is the sentinel for "no DB row yet" (preview mode), in which
// case {{EXEC_ID}} renders empty and {{OUTPUT_DIR}} stays empty.
func (r *AgentExecutor) TemplateVarsFor(opts RunOpts, startedAt time.Time, callID int64) TemplateVars {
	agentName := r.Agent.Name()
	model := agent.NormalizeModel(extractModel(r.Agent))
	return BuildTemplateVars(r, opts, startedAt, callID, agentName, model)
}

// BuildTemplateVars constructs a fully populated TemplateVars.
func BuildTemplateVars(r *AgentExecutor, opts RunOpts, startedAt time.Time, callID int64, agentName, model string) TemplateVars {
	vars := TemplateVars{
		ProjectName:             r.ProjectName,
		Role:                    opts.RoleID,
		Action:                  opts.Action,
		Batch:                   opts.Batch,
		Timestamp:               startedAt.Format(display.TimestampFormat),
		Profile:                 r.Profile,
		ExecID:                  callID,
		Agent:                   agentName,
		Model:                   model,
		Effort:                  r.Effort,
		MaxBudgetUSD:            r.MaxBudgetUSD,
		MaxBudgetUSDBatch:       r.MaxBudgetUSDBatch,
		SubRunArgs:              r.SubRunArgs,
		ContainerType:           r.ContainerType,
		ContainerName:           r.ContainerName,
		AutoRolesCommandsOutput: opts.AutoRolesCommandsOutput,
	}
	// ProjectFullPath / ProjectDir describe the project root, NOT the agent's
	// working directory. Derive from r.ProjectDir (.ateam path) so the values
	// stay anchored to the project even in remote-project mode where
	// r.SourceDir (agent cwd) points elsewhere. CONFIG.md documents these as
	// project-stable, and runtime.hcl uses them for container names, etc.
	switch {
	case r.ProjectDir != "":
		projectRoot := filepath.Dir(r.ProjectDir)
		vars.ProjectFullPath = projectRoot
		vars.ProjectDir = filepath.Base(projectRoot)
	case r.SourceDir != "":
		// No project context (ad-hoc exec / org-less): fall back to SourceDir.
		vars.ProjectFullPath = r.SourceDir
		vars.ProjectDir = filepath.Base(r.SourceDir)
	}
	if callID > 0 && r.StateDir() != "" {
		vars.OutputDir = runtimeDirFor(r.StateDir(), callID)
		if primary := PrimaryOutputName(opts.OutputKind, opts.PromptName); primary != "" {
			vars.OutputFile = filepath.Join(vars.OutputDir, primary)
		}
		// {{exec.prompt_file}} = absolute path to the archived prompt the
		// runner writes during ExecutePrepared. Codex et al. accept a
		// file path as the last positional arg, so this lets runtime.hcl
		// args be authored without the agent having to read stdin.
		vars.PromptFile = filepath.Join(logsDirFor(r.StateDir(), callID), PromptFileName)
	}
	return vars
}

// Per-exec_id forensic artifact filenames inside logs/<exec_id>/.
// Centralized so renames touch one constant set instead of ~30 sites
// across runner/web/flow; external orchestrators that grep for these
// names should treat them as wire surface.
const (
	AgentFileName    = "agent.jsonl"   // raw agent stream events
	BundleFileName   = "bundle.jsonl"  // flow lifecycle events
	CmdFileName      = "cmd.md"        // run-context summary
	PromptFileName   = "prompt.md"     // assembled prompt
	SettingsFileName = "settings.json" // sandbox settings
	StderrFileName   = "stderr.out"    // captured agent stderr
)

// RuntimeDirFor returns the per-exec_id agent-writable output directory.
// Exported so consumers (web/serve, flow reporters) can build paths
// without re-deriving from "/logs/" → "/runtime/" string surgery.
func RuntimeDirFor(stateDir string, execID int64) string {
	return filepath.Join(stateDir, "runtime", strconv.FormatInt(execID, 10))
}

// LogsDirFor returns the per-exec_id forensic log directory.
func LogsDirFor(stateDir string, execID int64) string {
	return filepath.Join(stateDir, "logs", strconv.FormatInt(execID, 10))
}

// internal shims preserved so the legacy local-camel-case call sites
// inside the runner package keep compiling without churn.
func runtimeDirFor(stateDir string, execID int64) string { return RuntimeDirFor(stateDir, execID) }
func logsDirFor(stateDir string, execID int64) string    { return LogsDirFor(stateDir, execID) }

// PrimaryOutputName maps an OutputKind to the canonical filename the agent
// writes for that action via {{OUTPUT_FILE}}. Returns "" when the action has
// no primary output.
//
// For OutputKindReport, the filename is `<promptName>.md` (e.g.
// `security.md` for the security role) so each role's report lands at a
// role-distinct path under shared/report/<role>/. Other kinds have a
// fixed filename and ignore promptName; OutputKindReport with an empty
// promptName falls back to the historical "report.md" so legacy callers
// that haven't been wired to set PromptName don't silently lose their
// output.
func PrimaryOutputName(kind, promptName string) string {
	switch kind {
	case OutputKindReport:
		if promptName != "" {
			return promptName + ".md"
		}
		return "report.md"
	case OutputKindReview:
		return "review.md"
	case OutputKindVerify:
		return "verify.md"
	case OutputKindExecutionReport:
		return "execution_report.md"
	case OutputKindSetupOverview:
		return "setup_overview.md"
	case OutputKindAutoRoles:
		return "auto_roles.md"
	default:
		return ""
	}
}

// OutputKind* enumerates the well-known primary outputs an action produces
// in runtime/<exec_id>/. Empty string means no primary output (run, parallel,
// auto-debug).
const (
	OutputKindReport          = "report"
	OutputKindReview          = "review"
	OutputKindVerify          = "verify"
	OutputKindExecutionReport = "execution_report"
	OutputKindSetupOverview   = "setup_overview"
	OutputKindAutoRoles       = "auto_roles"
)
