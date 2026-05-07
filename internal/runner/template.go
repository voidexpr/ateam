package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
)

// TemplateVars holds the variables available for {{VAR}} substitution
// in agent args and extra args from runtime.hcl.
type TemplateVars struct {
	ProjectName     string // from config.toml project.name
	ProjectFullPath string // absolute path to project source dir
	ProjectDir      string // last component of ProjectFullPath
	Role            string // role ID (e.g. "security", "supervisor")
	Action          string // action type (e.g. "report", "run", "code")
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
		ProjectName:   r.ProjectName,
		Role:          opts.RoleID,
		Action:        opts.Action,
		Batch:         opts.Batch,
		Timestamp:     startedAt.Format(TimestampFormat),
		Profile:       r.Profile,
		ExecID:        callID,
		Agent:         agentName,
		Model:         model,
		ContainerType: r.ContainerType,
		ContainerName: r.ContainerName,
	}
	if r.SourceDir != "" {
		vars.ProjectFullPath = r.SourceDir
		vars.ProjectDir = filepath.Base(r.SourceDir)
	}
	if callID > 0 && r.ProjectDir != "" {
		vars.OutputDir = runtimeDirFor(r.ProjectDir, callID)
		if primary := PrimaryOutputName(opts.OutputKind); primary != "" {
			vars.OutputFile = filepath.Join(vars.OutputDir, primary)
		}
	}
	return vars
}

// runtimeDirFor returns the per-exec_id agent-writable output directory.
// Mirrors root.ResolvedEnv.RuntimeDir but lives here to avoid a package
// cycle (root imports runner via cmd, not the reverse).
func runtimeDirFor(projectDir string, execID int64) string {
	return filepath.Join(projectDir, "runtime", fmt.Sprintf("%d", execID))
}

// logsDirFor returns the per-exec_id forensic log directory.
func logsDirFor(projectDir string, execID int64) string {
	return filepath.Join(projectDir, "logs", fmt.Sprintf("%d", execID))
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
)
