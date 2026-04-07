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
	TaskGroup       string // task group ID (e.g. "code-2026-03-31_06-09-39")
	Timestamp       string // run start time (TimestampFormat)
	Profile         string // active profile name
	ExecID          int64  // call tracking ID (from ateam ps)
	Agent           string // agent config name (e.g. "claude", "claude-docker")
	Model           string // resolved model name
	Container       string // container type ("none", "docker", etc.)
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
		"{{TASK_GROUP}}", v.TaskGroup,
		"{{TIMESTAMP}}", v.Timestamp,
		"{{PROFILE}}", v.Profile,
		"{{EXEC_ID}}", execID,
		"{{AGENT}}", v.Agent,
		"{{MODEL}}", v.Model,
		"{{CONTAINER}}", v.Container,
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

// resolveAgentTemplateArgs resolves templates in the agent's Args, Env values,
// and other string fields. Clones the agent so the original config is never mutated.
func resolveAgentTemplateArgs(a agent.Agent, vars TemplateVars) agent.Agent {
	r := vars.Replacer()
	switch t := a.(type) {
	case *agent.ClaudeAgent:
		clone := *t
		clone.Args = resolveArgs(t.Args, r)
		clone.Env = resolveMap(t.Env, r)
		return &clone
	case *agent.CodexAgent:
		clone := *t
		clone.Args = resolveArgs(t.Args, r)
		clone.Env = resolveMap(t.Env, r)
		return &clone
	}
	return a
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
// Mutates the container in place (ExtraArgs, ContainerName, Env, ExtraVolumes).
func resolveContainerTemplates(c container.Container, vars TemplateVars) {
	if c == nil {
		return
	}
	switch dc := c.(type) {
	case *container.DockerContainer:
		r := vars.Replacer()
		dc.ExtraArgs = resolveArgs(dc.ExtraArgs, r)
		dc.ExtraVolumes = resolveArgs(dc.ExtraVolumes, r)
		if strings.Contains(dc.ContainerName, "{{") {
			dc.ContainerName = r.Replace(dc.ContainerName)
		}
		dc.Env = resolveMap(dc.Env, r)
	}
}

// BuildTemplateVars constructs a fully populated TemplateVars.
func BuildTemplateVars(r *Runner, opts RunOpts, startedAt time.Time, callID int64, agentName, model string) TemplateVars {
	vars := TemplateVars{
		ProjectName: r.ProjectName,
		Role:        opts.RoleID,
		Action:      opts.Action,
		TaskGroup:   opts.TaskGroup,
		Timestamp:   startedAt.Format(TimestampFormat),
		Profile:     r.Profile,
		ExecID:      callID,
		Agent:       agentName,
		Model:       model,
		Container:   r.ContainerType,
	}
	if r.SourceDir != "" {
		vars.ProjectFullPath = r.SourceDir
		vars.ProjectDir = filepath.Base(r.SourceDir)
	}
	return vars
}
