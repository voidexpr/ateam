package runner

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
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

// ResolveTemplateArgs replaces {{VAR}} placeholders in each arg string.
// Unknown variables are left as-is.
func ResolveTemplateArgs(args []string, vars TemplateVars) []string {
	execID := ""
	if vars.ExecID > 0 {
		execID = fmt.Sprintf("%d", vars.ExecID)
	}
	replacements := []string{
		"{{PROJECT_NAME}}", vars.ProjectName,
		"{{PROJECT_FULL_PATH}}", vars.ProjectFullPath,
		"{{PROJECT_DIR}}", vars.ProjectDir,
		"{{ROLE}}", vars.Role,
		"{{ACTION}}", vars.Action,
		"{{TASK_GROUP}}", vars.TaskGroup,
		"{{TIMESTAMP}}", vars.Timestamp,
		"{{PROFILE}}", vars.Profile,
		"{{EXEC_ID}}", execID,
		"{{AGENT}}", vars.Agent,
		"{{MODEL}}", vars.Model,
		"{{CONTAINER}}", vars.Container,
	}
	replacer := strings.NewReplacer(replacements...)

	resolved := make([]string, len(args))
	for i, arg := range args {
		resolved[i] = replacer.Replace(arg)
	}
	return resolved
}

// resolveAgentTemplateArgs resolves templates in the agent's base Args on a
// per-run clone, so the original shared agent config is never mutated.
func resolveAgentTemplateArgs(a agent.Agent, vars TemplateVars) agent.Agent {
	switch t := a.(type) {
	case *agent.ClaudeAgent:
		clone := *t
		clone.Args = ResolveTemplateArgs(t.Args, vars)
		clone.Env = cloneStringMap(t.Env)
		return &clone
	case *agent.CodexAgent:
		clone := *t
		clone.Args = ResolveTemplateArgs(t.Args, vars)
		clone.Env = cloneStringMap(t.Env)
		return &clone
	}
	return a
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
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
