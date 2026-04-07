package runner

import (
	"testing"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
)

// --- ResolveTemplateArgs ---

func TestResolveTemplateArgsAllVars(t *testing.T) {
	vars := TemplateVars{
		ProjectName:     "myproject",
		ProjectFullPath: "/home/user/projects/myproject",
		ProjectDir:      "myproject",
		Role:            "security",
		Action:          "report",
		TaskGroup:       "code-2026-03-31_06-09-39",
		Timestamp:       "2026-03-31_06-09-39",
		Profile:         "docker",
		ExecID:          42,
		Agent:           "claude-docker",
		Model:           "sonnet",
		Container:       "docker",
	}

	tests := []struct {
		input string
		want  string
	}{
		{"{{PROJECT_NAME}}", "myproject"},
		{"{{PROJECT_FULL_PATH}}", "/home/user/projects/myproject"},
		{"{{PROJECT_DIR}}", "myproject"},
		{"{{ROLE}}", "security"},
		{"{{ACTION}}", "report"},
		{"{{TASK_GROUP}}", "code-2026-03-31_06-09-39"},
		{"{{TIMESTAMP}}", "2026-03-31_06-09-39"},
		{"{{PROFILE}}", "docker"},
		{"{{EXEC_ID}}", "42"},
		{"{{AGENT}}", "claude-docker"},
		{"{{MODEL}}", "sonnet"},
		{"{{CONTAINER}}", "docker"},
	}

	for _, tt := range tests {
		got := ResolveTemplateArgs([]string{tt.input}, vars)
		if got[0] != tt.want {
			t.Errorf("ResolveTemplateArgs(%q) = %q, want %q", tt.input, got[0], tt.want)
		}
	}
}

func TestResolveTemplateArgsMultipleVarsInOneString(t *testing.T) {
	vars := TemplateVars{
		ProjectDir: "myapp",
		Role:       "security",
		Action:     "report",
		ExecID:     7,
	}

	got := ResolveTemplateArgs([]string{"{{PROJECT_DIR}}-{{ROLE}}-{{ACTION}}-{{EXEC_ID}}"}, vars)
	if got[0] != "myapp-security-report-7" {
		t.Errorf("got %q, want %q", got[0], "myapp-security-report-7")
	}
}

func TestResolveTemplateArgsExecIDZero(t *testing.T) {
	vars := TemplateVars{ExecID: 0, Role: "security"}
	got := ResolveTemplateArgs([]string{"id={{EXEC_ID}}"}, vars)
	if got[0] != "id=" {
		t.Errorf("expected 'id=' for zero EXEC_ID, got %q", got[0])
	}
}

func TestResolveTemplateArgsNoTemplates(t *testing.T) {
	vars := TemplateVars{Role: "security"}
	args := []string{"-p", "--verbose", "--output-format", "stream-json"}
	got := ResolveTemplateArgs(args, vars)
	for i, arg := range args {
		if got[i] != arg {
			t.Errorf("arg[%d] changed: got %q, want %q", i, got[i], arg)
		}
	}
}

func TestResolveTemplateArgsUnknownVar(t *testing.T) {
	got := ResolveTemplateArgs([]string{"{{UNKNOWN_VAR}}"}, TemplateVars{})
	if got[0] != "{{UNKNOWN_VAR}}" {
		t.Errorf("unknown var should be preserved: got %q", got[0])
	}
}

func TestResolveTemplateArgsEmptySlice(t *testing.T) {
	got := ResolveTemplateArgs(nil, TemplateVars{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestResolveTemplateArgsEmptyVarValues(t *testing.T) {
	// All vars empty/zero — templates resolve to empty strings.
	got := ResolveTemplateArgs([]string{"{{ROLE}}-{{ACTION}}"}, TemplateVars{})
	if got[0] != "-" {
		t.Errorf("expected '-', got %q", got[0])
	}
}

func TestResolveTemplateArgsDoesNotMutateInput(t *testing.T) {
	args := []string{"{{ROLE}}", "static"}
	original := make([]string, len(args))
	copy(original, args)

	ResolveTemplateArgs(args, TemplateVars{Role: "security"})

	for i := range args {
		if args[i] != original[i] {
			t.Errorf("input arg[%d] mutated: got %q, want %q", i, args[i], original[i])
		}
	}
}

// --- ResolveTemplateString ---

func TestResolveTemplateString(t *testing.T) {
	vars := TemplateVars{ProjectDir: "myapp", Role: "security"}
	got := ResolveTemplateString("ateam-{{PROJECT_DIR}}-{{ROLE}}", vars)
	if got != "ateam-myapp-security" {
		t.Errorf("got %q, want %q", got, "ateam-myapp-security")
	}
}

func TestResolveTemplateStringNoTemplate(t *testing.T) {
	got := ResolveTemplateString("plain-string", TemplateVars{Role: "x"})
	if got != "plain-string" {
		t.Errorf("got %q, want %q", got, "plain-string")
	}
}

func TestResolveTemplateStringEmpty(t *testing.T) {
	got := ResolveTemplateString("", TemplateVars{})
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- ResolveTemplateMap ---

func TestResolveTemplateMap(t *testing.T) {
	vars := TemplateVars{Role: "security", ProjectDir: "myapp", ExecID: 42}
	m := map[string]string{
		"ATEAM_ROLE":    "{{ROLE}}",
		"ATEAM_SESSION": "{{PROJECT_DIR}}-{{EXEC_ID}}",
		"STATIC":        "unchanged",
	}

	got := ResolveTemplateMap(m, vars)
	if got["ATEAM_ROLE"] != "security" {
		t.Errorf("ATEAM_ROLE: got %q, want %q", got["ATEAM_ROLE"], "security")
	}
	if got["ATEAM_SESSION"] != "myapp-42" {
		t.Errorf("ATEAM_SESSION: got %q, want %q", got["ATEAM_SESSION"], "myapp-42")
	}
	if got["STATIC"] != "unchanged" {
		t.Errorf("STATIC: got %q, want %q", got["STATIC"], "unchanged")
	}
}

func TestResolveTemplateMapDoesNotMutateInput(t *testing.T) {
	m := map[string]string{"KEY": "{{ROLE}}"}
	ResolveTemplateMap(m, TemplateVars{Role: "security"})
	if m["KEY"] != "{{ROLE}}" {
		t.Errorf("original map mutated: got %q", m["KEY"])
	}
}

func TestResolveTemplateMapNil(t *testing.T) {
	got := ResolveTemplateMap(nil, TemplateVars{})
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestResolveTemplateMapEmpty(t *testing.T) {
	got := ResolveTemplateMap(map[string]string{}, TemplateVars{})
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestResolveTemplateMapKeysNotResolved(t *testing.T) {
	m := map[string]string{"{{ROLE}}": "value"}
	got := ResolveTemplateMap(m, TemplateVars{Role: "security"})
	if _, ok := got["{{ROLE}}"]; !ok {
		t.Error("map key should not be resolved")
	}
	if _, ok := got["security"]; ok {
		t.Error("resolved key should not appear")
	}
}

// --- resolveAgentTemplateArgs ---

func TestResolveAgentTemplateArgsClaude(t *testing.T) {
	a := &agent.ClaudeAgent{
		Command: "claude",
		Args:    []string{"-p", "--name", "{{PROJECT_DIR}}-{{ROLE}}"},
		Env:     map[string]string{"SESSION": "{{EXEC_ID}}", "STATIC": "val"},
	}
	vars := TemplateVars{ProjectDir: "myapp", Role: "security", ExecID: 99}

	resolved := resolveAgentTemplateArgs(a, vars)
	got := resolved.(*agent.ClaudeAgent)

	if got.Args[2] != "myapp-security" {
		t.Errorf("Args: got %q, want %q", got.Args[2], "myapp-security")
	}
	if got.Env["SESSION"] != "99" {
		t.Errorf("Env SESSION: got %q, want %q", got.Env["SESSION"], "99")
	}
	if got.Env["STATIC"] != "val" {
		t.Errorf("Env STATIC: got %q, want %q", got.Env["STATIC"], "val")
	}
}

func TestResolveAgentTemplateArgsCodex(t *testing.T) {
	a := &agent.CodexAgent{
		Command: "codex",
		Args:    []string{"--name", "{{ROLE}}-{{ACTION}}"},
		Env:     map[string]string{"ROLE": "{{ROLE}}"},
	}
	vars := TemplateVars{Role: "testing_basic", Action: "run"}

	resolved := resolveAgentTemplateArgs(a, vars)
	got := resolved.(*agent.CodexAgent)

	if got.Args[1] != "testing_basic-run" {
		t.Errorf("Args: got %q, want %q", got.Args[1], "testing_basic-run")
	}
	if got.Env["ROLE"] != "testing_basic" {
		t.Errorf("Env ROLE: got %q, want %q", got.Env["ROLE"], "testing_basic")
	}
}

func TestResolveAgentTemplateArgsDoesNotMutateOriginal(t *testing.T) {
	original := &agent.ClaudeAgent{
		Command: "claude",
		Args:    []string{"--name", "{{ROLE}}"},
		Env:     map[string]string{"K": "{{ROLE}}"},
	}
	vars := TemplateVars{Role: "security"}

	resolved := resolveAgentTemplateArgs(original, vars)

	// Original unchanged
	if original.Args[1] != "{{ROLE}}" {
		t.Errorf("original Args mutated: got %q", original.Args[1])
	}
	if original.Env["K"] != "{{ROLE}}" {
		t.Errorf("original Env mutated: got %q", original.Env["K"])
	}

	// Resolved is different object
	got := resolved.(*agent.ClaudeAgent)
	if got == original {
		t.Error("resolved should be a different pointer than original")
	}
	if got.Args[1] != "security" {
		t.Errorf("resolved Args: got %q, want %q", got.Args[1], "security")
	}
}

func TestResolveAgentTemplateArgsNilEnv(t *testing.T) {
	a := &agent.ClaudeAgent{
		Command: "claude",
		Args:    []string{"--name", "{{ROLE}}"},
		// Env is nil
	}
	vars := TemplateVars{Role: "security"}

	resolved := resolveAgentTemplateArgs(a, vars)
	got := resolved.(*agent.ClaudeAgent)

	if got.Env != nil {
		t.Errorf("expected nil Env, got %v", got.Env)
	}
	if got.Args[1] != "security" {
		t.Errorf("Args: got %q, want %q", got.Args[1], "security")
	}
}

func TestResolveAgentTemplateArgsMockAgent(t *testing.T) {
	// MockAgent doesn't implement Args — should be returned as-is.
	a := &agent.MockAgent{}
	vars := TemplateVars{Role: "security"}
	resolved := resolveAgentTemplateArgs(a, vars)
	if resolved != a {
		t.Error("unknown agent type should be returned as-is")
	}
}

// --- resolveContainerTemplates ---

func TestResolveContainerTemplatesDocker(t *testing.T) {
	dc := &container.DockerContainer{
		ExtraArgs:     []string{"--hostname", "{{PROJECT_DIR}}-{{ROLE}}", "--label", "action={{ACTION}}"},
		ContainerName: "ateam-{{PROJECT_DIR}}-{{ROLE}}",
		ExtraVolumes:  []string{"{{PROJECT_FULL_PATH}}/data:/data:ro", "/static:/static:ro"},
		Env:           map[string]string{"SESSION": "{{EXEC_ID}}", "PLAIN": "value"},
	}
	vars := TemplateVars{
		ProjectDir:      "myapp",
		ProjectFullPath: "/home/user/myapp",
		Role:            "security",
		Action:          "report",
		ExecID:          7,
	}

	resolveContainerTemplates(dc, vars)

	// ExtraArgs
	if dc.ExtraArgs[1] != "myapp-security" {
		t.Errorf("ExtraArgs[1]: got %q, want %q", dc.ExtraArgs[1], "myapp-security")
	}
	if dc.ExtraArgs[3] != "action=report" {
		t.Errorf("ExtraArgs[3]: got %q, want %q", dc.ExtraArgs[3], "action=report")
	}

	// ContainerName
	if dc.ContainerName != "ateam-myapp-security" {
		t.Errorf("ContainerName: got %q, want %q", dc.ContainerName, "ateam-myapp-security")
	}

	// ExtraVolumes
	if dc.ExtraVolumes[0] != "/home/user/myapp/data:/data:ro" {
		t.Errorf("ExtraVolumes[0]: got %q, want %q", dc.ExtraVolumes[0], "/home/user/myapp/data:/data:ro")
	}
	if dc.ExtraVolumes[1] != "/static:/static:ro" {
		t.Errorf("ExtraVolumes[1] should be unchanged: got %q", dc.ExtraVolumes[1])
	}

	// Env
	if dc.Env["SESSION"] != "7" {
		t.Errorf("Env SESSION: got %q, want %q", dc.Env["SESSION"], "7")
	}
	if dc.Env["PLAIN"] != "value" {
		t.Errorf("Env PLAIN: got %q, want %q", dc.Env["PLAIN"], "value")
	}
}

func TestResolveContainerTemplatesNoTemplates(t *testing.T) {
	dc := &container.DockerContainer{
		ExtraArgs:     []string{"--privileged"},
		ContainerName: "ateam-static",
		ExtraVolumes:  []string{"/data:/data:ro"},
	}
	resolveContainerTemplates(dc, TemplateVars{Role: "security"})

	if dc.ExtraArgs[0] != "--privileged" {
		t.Errorf("ExtraArgs should be unchanged: got %q", dc.ExtraArgs[0])
	}
	if dc.ContainerName != "ateam-static" {
		t.Errorf("ContainerName should be unchanged: got %q", dc.ContainerName)
	}
}

func TestResolveContainerTemplatesEmptyFields(t *testing.T) {
	dc := &container.DockerContainer{}
	resolveContainerTemplates(dc, TemplateVars{Role: "security"})
	// Should not panic on empty/nil fields.
	if dc.ContainerName != "" {
		t.Errorf("expected empty ContainerName, got %q", dc.ContainerName)
	}
}

func TestResolveContainerTemplatesNilContainer(t *testing.T) {
	resolveContainerTemplates(nil, TemplateVars{})
}

func TestResolveContainerTemplatesNonDocker(t *testing.T) {
	// DevcontainerContainer should be ignored (no-op).
	dc := &container.DevcontainerContainer{
		ConfigPath: "{{ROLE}}",
	}
	resolveContainerTemplates(dc, TemplateVars{Role: "security"})
	if dc.ConfigPath != "{{ROLE}}" {
		t.Errorf("DevcontainerContainer fields should not be resolved: got %q", dc.ConfigPath)
	}
}

// --- BuildTemplateVars ---

func TestBuildTemplateVars(t *testing.T) {
	r := &Runner{
		ProjectName:   "myproject",
		SourceDir:     "/home/user/projects/myproject",
		Profile:       "docker",
		ContainerType: "docker",
	}
	opts := RunOpts{
		RoleID:    "security",
		Action:    "report",
		TaskGroup: "code-2026-01-01_00-00-00",
	}
	ts := time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC)

	vars := BuildTemplateVars(r, opts, ts, 42, "claude-docker", "sonnet")

	if vars.ProjectName != "myproject" {
		t.Errorf("ProjectName: got %q", vars.ProjectName)
	}
	if vars.ProjectFullPath != "/home/user/projects/myproject" {
		t.Errorf("ProjectFullPath: got %q", vars.ProjectFullPath)
	}
	if vars.ProjectDir != "myproject" {
		t.Errorf("ProjectDir: got %q", vars.ProjectDir)
	}
	if vars.Role != "security" {
		t.Errorf("Role: got %q", vars.Role)
	}
	if vars.Action != "report" {
		t.Errorf("Action: got %q", vars.Action)
	}
	if vars.TaskGroup != "code-2026-01-01_00-00-00" {
		t.Errorf("TaskGroup: got %q", vars.TaskGroup)
	}
	if vars.Timestamp != "2026-01-01_12-30-00" {
		t.Errorf("Timestamp: got %q", vars.Timestamp)
	}
	if vars.Profile != "docker" {
		t.Errorf("Profile: got %q", vars.Profile)
	}
	if vars.ExecID != 42 {
		t.Errorf("ExecID: got %d", vars.ExecID)
	}
	if vars.Agent != "claude-docker" {
		t.Errorf("Agent: got %q", vars.Agent)
	}
	if vars.Model != "sonnet" {
		t.Errorf("Model: got %q", vars.Model)
	}
	if vars.Container != "docker" {
		t.Errorf("Container: got %q", vars.Container)
	}
}

func TestBuildTemplateVarsEmptySourceDir(t *testing.T) {
	r := &Runner{}
	opts := RunOpts{RoleID: "security", Action: "report"}
	ts := time.Now()

	vars := BuildTemplateVars(r, opts, ts, 0, "claude", "")

	if vars.ProjectFullPath != "" {
		t.Errorf("ProjectFullPath should be empty: got %q", vars.ProjectFullPath)
	}
	if vars.ProjectDir != "" {
		t.Errorf("ProjectDir should be empty: got %q", vars.ProjectDir)
	}
}
