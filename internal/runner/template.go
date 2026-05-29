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
	ProjectName     string // from config.toml project.name
	ProjectFullPath string // absolute path to project source dir
	ProjectDir      string // last component of ProjectFullPath
	Role            string // role ID (e.g. "security", "supervisor")
	Action          string // action type (e.g. "report", "exec", "code")
	Batch           string // batch ID (e.g. "code-2026-03-31_06-09-39")
	Timestamp       string // run start time (TimestampFormat)
	Profile         string // active profile name
	ExecID          int64  // call tracking ID (from ateam ps)
	Agent           string // agent config name (e.g. "claude", "claude-docker")
	Model           string // resolved model name
	ContainerType   string // container type ("none", "docker", "docker-exec", etc.)
	ContainerName   string // docker container name (e.g. "ateam-myapp-security")
	OutputDir       string // absolute path to <projectDir>/runtime/<exec_id>/ (where the agent should write files)
	OutputFile      string // absolute path to OutputDir/<primary_kind> (e.g. report.md); empty when the action has no primary output

	// AutoRolesCommandsOutput is the pre-baked context bundle injected into
	// `{{ATEAM_AUTO_ROLES_COMMANDS_OUTPUT}}` for the --auto-roles planner agent.
	// Copied from RunOpts.AutoRolesCommandsOutput; empty for every other action.
	AutoRolesCommandsOutput string
}

// Replacer builds a strings.Replacer for the current template vars.
// Call once per Run() and reuse across all resolution calls.
func (v TemplateVars) Replacer() *strings.Replacer {
	execID := ""
	if v.ExecID > 0 {
		execID = fmt.Sprintf("%d", v.ExecID)
	}
	return strings.NewReplacer(
		"{{PROJECT_NAME}}", v.ProjectName,
		"{{PROJECT_FULL_PATH}}", v.ProjectFullPath,
		"{{PROJECT_DIR}}", v.ProjectDir,
		"{{ROLE}}", v.Role,
		"{{ACTION}}", v.Action,
		"{{BATCH}}", v.Batch,
		"{{TIMESTAMP}}", v.Timestamp,
		"{{PROFILE}}", v.Profile,
		"{{EXEC_ID}}", execID,
		"{{AGENT}}", v.Agent,
		"{{MODEL}}", v.Model,
		"{{CONTAINER_TYPE}}", v.ContainerType,
		"{{CONTAINER_NAME}}", v.ContainerName,
		"{{OUTPUT_DIR}}", v.OutputDir,
		"{{OUTPUT_FILE}}", v.OutputFile,
		// Legacy alias for code_management_prompt.md; same dir as OUTPUT_DIR.
		"{{EXECUTION_DIR}}", v.OutputDir,
		// ateam's own docs, embedded in the binary. Prompts that need
		// ateam-specific knowledge (commands, config, isolation, roles) can
		// inline these instead of asking the agent to grep the host project.
		"{{ATEAM_OWN_README}}", defaults.SelfDocs["README"],
		"{{ATEAM_OWN_COMMANDS}}", defaults.SelfDocs["COMMANDS"],
		"{{ATEAM_OWN_CONFIG}}", defaults.SelfDocs["CONFIG"],
		"{{ATEAM_OWN_ISOLATION}}", defaults.SelfDocs["ISOLATION"],
		"{{ATEAM_OWN_ROLES}}", defaults.SelfDocs["ROLES"],
		// Contract marker for the --auto-roles planner output; same constant
		// is read back by cmd/auto_roles.go::parseAutoRolesOutput.
		"{{AUTO_ROLES_MARKER}}", prompts.AutoRolesMarker,
		// Pre-baked context bundle for the --auto-roles planner agent. Computed
		// by cmd/auto_roles.go before the run; empty for every other action.
		"{{ATEAM_AUTO_ROLES_COMMANDS_OUTPUT}}", v.AutoRolesCommandsOutput,
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

// BuildTemplateVars constructs a fully populated TemplateVars.
func BuildTemplateVars(r *Runner, opts RunOpts, startedAt time.Time, callID int64, agentName, model string) TemplateVars {
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
		if primary := PrimaryOutputName(opts.OutputKind); primary != "" {
			vars.OutputFile = filepath.Join(vars.OutputDir, primary)
		}
	}
	return vars
}

// runtimeDirFor returns the per-exec_id agent-writable output directory.
// Lives here (rather than only on root.ResolvedEnv) so the runner can build
// paths from just ProjectDir without taking a dependency on the root package.
func runtimeDirFor(projectDir string, execID int64) string {
	return filepath.Join(projectDir, "runtime", strconv.FormatInt(execID, 10))
}

// logsDirFor returns the per-exec_id forensic log directory.
func logsDirFor(projectDir string, execID int64) string {
	return filepath.Join(projectDir, "logs", strconv.FormatInt(execID, 10))
}

// PrimaryOutputName maps an OutputKind to the canonical filename the agent
// writes for that action via {{OUTPUT_FILE}}. Returns "" when the action has
// no primary output.
func PrimaryOutputName(kind string) string {
	switch kind {
	case OutputKindReport:
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
