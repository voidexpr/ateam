# Org/Project Split Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Split `.ateam` into `.ateamorg` (organization defaults) and `.ateam` (project config/results), with 3-level prompt fallback and multi-project support.

**Architecture:** Rewrite internal packages bottom-up: config → embed → root resolution → init functions → prompt assembly → commands. Each layer is testable independently. All tests use `./test_data/` directory for fixtures created/destroyed within tests using `t.TempDir()` or manual setup under `test_data/`.

**Tech Stack:** Go 1.23, cobra, BurntSushi/toml, embed FS

---

### Task 1: Rewrite Config Struct

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	cfg := Config{
		Project: ProjectConfig{Name: "myproj", Source: "level1/myproj"},
		Git:     GitConfig{Repo: ".", RemoteOriginURL: "https://foobar/myproj.git"},
		Report:  ReportConfig{MaxParallel: 3, AgentReportTimeoutMinutes: 10},
		Agents:  map[string]string{"security": "enabled", "testing_basic": "disabled"},
	}

	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Project.Name != "myproj" {
		t.Errorf("Project.Name = %q, want %q", got.Project.Name, "myproj")
	}
	if got.Project.Source != "level1/myproj" {
		t.Errorf("Project.Source = %q, want %q", got.Project.Source, "level1/myproj")
	}
	if got.Git.Repo != "." {
		t.Errorf("Git.Repo = %q, want %q", got.Git.Repo, ".")
	}
	if got.Git.RemoteOriginURL != "https://foobar/myproj.git" {
		t.Errorf("Git.RemoteOriginURL = %q", got.Git.RemoteOriginURL)
	}
	if got.Report.MaxParallel != 3 {
		t.Errorf("Report.MaxParallel = %d, want 3", got.Report.MaxParallel)
	}
	if got.Agents["security"] != "enabled" {
		t.Errorf("Agents[security] = %q, want enabled", got.Agents["security"])
	}
	if got.Agents["testing_basic"] != "disabled" {
		t.Errorf("Agents[testing_basic] = %q, want disabled", got.Agents["testing_basic"])
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `[project]
name = "test"
source = "."

[git]
repo = "."

[agents]
security = "enabled"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(tomlContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Report.MaxParallel != DefaultMaxParallel {
		t.Errorf("MaxParallel = %d, want default %d", cfg.Report.MaxParallel, DefaultMaxParallel)
	}
	if cfg.Report.AgentReportTimeoutMinutes != DefaultAgentReportTimeoutMinutes {
		t.Errorf("Timeout = %d, want default %d", cfg.Report.AgentReportTimeoutMinutes, DefaultAgentReportTimeoutMinutes)
	}
}

func TestEnabledAgents(t *testing.T) {
	cfg := Config{
		Agents: map[string]string{
			"security":      "enabled",
			"testing_basic": "enabled",
			"automation":    "disabled",
		},
	}

	got := cfg.EnabledAgents()
	if len(got) != 2 {
		t.Fatalf("EnabledAgents() = %v, want 2 agents", got)
	}
	// Should be sorted
	if got[0] != "security" || got[1] != "testing_basic" {
		t.Errorf("EnabledAgents() = %v, want [security testing_basic]", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — new fields don't exist yet

**Step 3: Rewrite config.go**

Replace `internal/config/config.go` with:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

const (
	DefaultMaxParallel              = 3
	DefaultAgentReportTimeoutMinutes = 10
)

type Config struct {
	Project ProjectConfig     `toml:"project"`
	Git     GitConfig         `toml:"git"`
	Report  ReportConfig      `toml:"report"`
	Review  ReviewConfig      `toml:"review"`
	Code    CodeConfig        `toml:"code"`
	Agents  map[string]string `toml:"agents"`
}

type ProjectConfig struct {
	Name   string `toml:"name"`
	Source string `toml:"source"`
}

type GitConfig struct {
	Repo            string `toml:"repo"`
	RemoteOriginURL string `toml:"remote_origin_url"`
}

type ReportConfig struct {
	MaxParallel              int `toml:"max_parallel"`
	AgentReportTimeoutMinutes int `toml:"agent_report_timeout_minutes"`
}

type ReviewConfig struct{}

type CodeConfig struct{}

func DefaultConfig(name, source string, agents map[string]string) Config {
	return Config{
		Project: ProjectConfig{Name: name, Source: source},
		Report: ReportConfig{
			MaxParallel:              DefaultMaxParallel,
			AgentReportTimeoutMinutes: DefaultAgentReportTimeoutMinutes,
		},
		Agents: agents,
	}
}

func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config.toml: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config.toml: %w", err)
	}
	if cfg.Report.MaxParallel == 0 {
		cfg.Report.MaxParallel = DefaultMaxParallel
	}
	if cfg.Report.AgentReportTimeoutMinutes == 0 {
		cfg.Report.AgentReportTimeoutMinutes = DefaultAgentReportTimeoutMinutes
	}
	if cfg.Agents == nil {
		cfg.Agents = make(map[string]string)
	}
	return &cfg, nil
}

func (r ReportConfig) EffectiveTimeout(override int) int {
	if override > 0 {
		return override
	}
	return r.AgentReportTimeoutMinutes
}

func (c Config) EnabledAgents() []string {
	var enabled []string
	for name, status := range c.Agents {
		if status == "enabled" {
			enabled = append(enabled, name)
		}
	}
	sort.Strings(enabled)
	return enabled
}

func Save(dir string, cfg Config) error {
	path := filepath.Join(dir, "config.toml")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("cannot create config.toml: %w", err)
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	return enc.Encode(cfg)
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/
git commit -m 'config: rewrite struct for org/project split'
```

---

### Task 2: Restructure Embedded Defaults

**Files:**
- Rename: `internal/prompts/defaults/report_instructions.md` → `internal/prompts/defaults/report_prompt.md`
- Rename: `internal/prompts/defaults/apply_instructions.md` → `internal/prompts/defaults/code_prompt.md`
- Create: `internal/prompts/defaults/supervisor/report_commissioning_prompt.md` (placeholder)
- Modify: `internal/prompts/embed.go`

**Step 1: Rename embedded files**

```bash
cd internal/prompts/defaults
mv report_instructions.md report_prompt.md
mv apply_instructions.md code_prompt.md
```

**Step 2: Create supervisor placeholder**

Create `internal/prompts/defaults/supervisor/report_commissioning_prompt.md`:

```markdown
# Report Commissioning

You are the supervisor responsible for commissioning agent reports.

Review the available agents and determine which ones should produce reports for this project based on its characteristics.

Consider:
- Project type and technology stack
- Current project maturity
- Areas most likely to benefit from analysis

Produce a list of agents to run with brief justification for each.
```

**Step 3: Update embed.go constants and embed directive**

Replace the constants in `internal/prompts/prompts.go`:

```go
const (
	ReportPromptFile      = "report_prompt.md"
	CodePromptFile        = "code_prompt.md"
	ExtraReportPromptFile = "extra_report_prompt.md"
	ReviewPromptFile      = "review_prompt.md"
	ReportCommissioningPromptFile = "report_commissioning_prompt.md"
	FullReportFile        = "full_report.md"
)
```

Remove the old constants: `ReportInstructionsFile`, `ExtraReportPromptFile` stays.

Update `internal/prompts/embed.go`:

Change the embed directive from:
```go
//go:embed defaults/agents/*/report_prompt.md defaults/report_instructions.md defaults/supervisor/review_prompt.md
```
to:
```go
//go:embed defaults/agents/*/report_prompt.md defaults/agents/*/code_prompt.md defaults/report_prompt.md defaults/code_prompt.md defaults/supervisor/review_prompt.md defaults/supervisor/report_commissioning_prompt.md
```

Note: `defaults/agents/*/code_prompt.md` will only match agents that have one (currently just `refactor_small`). This is fine — the embed glob is optional per-agent.

Update helper functions:
```go
func DefaultReportPrompt() string {
	return readEmbedded("defaults/report_prompt.md")
}

func DefaultCodePrompt() string {
	return readEmbedded("defaults/code_prompt.md")
}

func DefaultSupervisorReviewPrompt() string {
	return readEmbedded("defaults/supervisor/review_prompt.md")
}

func DefaultSupervisorCommissioningPrompt() string {
	return readEmbedded("defaults/supervisor/report_commissioning_prompt.md")
}
```

Remove `DefaultReportInstructions()`.

Update `embeddedFiles()` to use new file names:
```go
func embeddedFiles() []embeddedFile {
	var files []embeddedFile
	for _, id := range AllAgentIDs {
		files = append(files, embeddedFile{
			filepath.Join("defaults", "agents", id, ReportPromptFile),
			DefaultAgentPrompt(id),
		})
		// code_prompt.md is optional per agent
		codePath := fmt.Sprintf("defaults/agents/%s/%s", id, CodePromptFile)
		if data, err := defaultsFS.ReadFile(codePath); err == nil {
			files = append(files, embeddedFile{
				filepath.Join("defaults", "agents", id, CodePromptFile),
				string(data),
			})
		}
	}
	files = append(files, embeddedFile{
		filepath.Join("defaults", ReportPromptFile),
		DefaultReportPrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", CodePromptFile),
		DefaultCodePrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", "supervisor", ReviewPromptFile),
		DefaultSupervisorReviewPrompt(),
	})
	files = append(files, embeddedFile{
		filepath.Join("defaults", "supervisor", ReportCommissioningPromptFile),
		DefaultSupervisorCommissioningPrompt(),
	})
	return files
}
```

**Step 4: Update prompts.go references**

In `AssembleAgentPrompt`, change `ReportInstructionsFile` reference:
```go
instructions := readFileOr(
	filepath.Join(ateamRoot, "defaults", ReportPromptFile),  // was ReportInstructionsFile
	"",
)
```

**Step 5: Build to verify**

Run: `make build`
Expected: compiles without errors

**Step 6: Commit**

```bash
git add internal/prompts/
git commit -m 'prompts: restructure embedded defaults for org/project split'
```

---

### Task 3: Rewrite Root Resolution

**Files:**
- Rewrite: `internal/root/resolve.go`
- Create: `internal/root/resolve_test.go`

**Step 1: Write the failing test**

Create `internal/root/resolve_test.go`:

```go
package root

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindOrg(t *testing.T) {
	base := t.TempDir()

	// Create .ateamorg at base
	orgDir := filepath.Join(base, ".ateamorg")
	os.MkdirAll(filepath.Join(orgDir, "defaults"), 0755)

	tests := []struct {
		name    string
		cwd     string
		want    string
		wantErr bool
	}{
		{"from parent", base, orgDir, false},
		{"from child", filepath.Join(base, "child"), orgDir, false},
		{"from inside org", orgDir, orgDir, false},
		{"from inside org subdir", filepath.Join(orgDir, "defaults"), orgDir, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.MkdirAll(tt.cwd, 0755)
			got, err := FindOrg(tt.cwd)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("FindOrg(%s) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}

func TestFindProject(t *testing.T) {
	base := t.TempDir()

	// Create .ateam project at base/myproj
	projParent := filepath.Join(base, "myproj")
	projDir := filepath.Join(projParent, ".ateam")
	os.MkdirAll(projDir, 0755)
	os.WriteFile(filepath.Join(projDir, "config.toml"), []byte("[project]\nname = \"myproj\"\n"), 0644)

	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"from project parent", projParent, projDir},
		{"from child of project parent", filepath.Join(projParent, "src"), projDir},
		{"from inside .ateam", projDir, projDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.MkdirAll(tt.cwd, 0755)
			got, err := FindProject(tt.cwd)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("FindProject(%s) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}

func TestFindOrgNotFound(t *testing.T) {
	base := t.TempDir()
	_, err := FindOrg(base)
	if err == nil {
		t.Fatal("expected error when no .ateamorg exists")
	}
}

func TestFindProjectNotFound(t *testing.T) {
	base := t.TempDir()
	_, err := FindProject(base)
	if err == nil {
		t.Fatal("expected error when no .ateam exists")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/root/ -run 'TestFind' -v`
Expected: FAIL — `FindOrg` and `FindProject` don't exist

**Step 3: Rewrite resolve.go**

Replace `internal/root/resolve.go` with:

```go
package root

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/config"
)

const (
	OrgDirName     = ".ateamorg"
	ProjectDirName = ".ateam"
)

// ResolvedEnv holds the resolved organization and project paths.
type ResolvedEnv struct {
	OrgDir      string         // absolute path to .ateamorg/
	ProjectDir  string         // absolute path to .ateam/
	ProjectName string         // from config.toml
	SourceDir   string         // resolved from config project.source
	GitRepoDir  string         // resolved from config git.repo
	Config      *config.Config
}

// AgentReportPath returns the path to an agent's report file.
func (e *ResolvedEnv) AgentReportPath(agentID, reportType string) string {
	return filepath.Join(e.ProjectDir, "agents", agentID, reportType+"_report.md")
}

// AgentHistoryDir returns the history directory for an agent.
func (e *ResolvedEnv) AgentHistoryDir(agentID string) string {
	return filepath.Join(e.ProjectDir, "agents", agentID, "history")
}

// ReviewPath returns the path to the supervisor review file.
func (e *ResolvedEnv) ReviewPath() string {
	return filepath.Join(e.ProjectDir, "supervisor", "review.md")
}

// ReviewHistoryDir returns the history directory for supervisor reviews.
func (e *ResolvedEnv) ReviewHistoryDir() string {
	return filepath.Join(e.ProjectDir, "supervisor", "history")
}

// FindOrg walks up from cwd looking for .ateamorg.
// Checks if cwd is inside .ateamorg first, then walks up looking for a sibling.
func FindOrg(cwd string) (string, error) {
	cwd = realPath(cwd)

	// Check if we're inside .ateamorg
	if orgDir, ok := findInPath(cwd, OrgDirName); ok {
		return orgDir, nil
	}

	// Walk up looking for .ateamorg child
	dir := cwd
	for {
		candidate := filepath.Join(dir, OrgDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return realPath(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no %s/ found (run 'ateam install' first)", OrgDirName)
}

// FindProject walks up from cwd looking for .ateam.
// Checks if cwd is inside .ateam first, then walks up looking for a sibling.
func FindProject(cwd string) (string, error) {
	cwd = realPath(cwd)

	// Check if we're inside .ateam
	if projDir, ok := findInPath(cwd, ProjectDirName); ok {
		return projDir, nil
	}

	// Walk up looking for .ateam child
	dir := cwd
	for {
		candidate := filepath.Join(dir, ProjectDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return realPath(candidate), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", fmt.Errorf("no %s/ found (run 'ateam init' first)", ProjectDirName)
}

// findInPath checks if cwd is inside a directory named target.
// Returns the target directory path if found.
func findInPath(cwd, target string) (string, bool) {
	parts := strings.Split(filepath.Clean(cwd), string(filepath.Separator))
	for i, part := range parts {
		if part == target {
			root := string(filepath.Separator) + filepath.Join(parts[:i+1]...)
			return root, true
		}
	}
	return "", false
}

// Resolve discovers the org and project, returning a combined environment.
// orgOverride and projectOverride are from -o and -p flags.
func Resolve(orgOverride, projectOverride string) (*ResolvedEnv, error) {
	cwd := realPath(mustGetwd())

	var orgDir string
	var err error
	if orgOverride != "" {
		orgDir, err = resolveOrgByName(orgOverride)
	} else {
		orgDir, err = FindOrg(cwd)
	}
	if err != nil {
		return nil, err
	}

	var projectDir string
	if projectOverride != "" {
		projectDir, err = resolveProjectByName(orgDir, projectOverride)
	} else {
		projectDir, err = FindProject(cwd)
	}
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(projectDir)
	if err != nil {
		return nil, err
	}

	env := &ResolvedEnv{
		OrgDir:      orgDir,
		ProjectDir:  projectDir,
		ProjectName: cfg.Project.Name,
		Config:      cfg,
	}

	if cfg.Project.Source != "" {
		env.SourceDir = resolveRelPath(projectDir, cfg.Project.Source)
	}
	if cfg.Git.Repo != "" && env.SourceDir != "" {
		env.GitRepoDir = resolveRelPath(env.SourceDir, cfg.Git.Repo)
	}

	return env, nil
}

// Lookup discovers org and project without creating anything. For env command.
func Lookup() (*ResolvedEnv, error) {
	cwd := realPath(mustGetwd())

	env := &ResolvedEnv{}

	orgDir, err := FindOrg(cwd)
	if err != nil {
		return nil, err
	}
	env.OrgDir = orgDir

	projectDir, _ := FindProject(cwd)
	if projectDir == "" {
		return env, nil
	}
	env.ProjectDir = projectDir

	cfg, err := config.Load(projectDir)
	if err != nil {
		return env, nil
	}
	env.Config = cfg
	env.ProjectName = cfg.Project.Name

	if cfg.Project.Source != "" {
		env.SourceDir = resolveRelPath(projectDir, cfg.Project.Source)
	}
	if cfg.Git.Repo != "" && env.SourceDir != "" {
		env.GitRepoDir = resolveRelPath(env.SourceDir, cfg.Git.Repo)
	}

	return env, nil
}

// resolveOrgByName finds an org directory by walking up and matching name.
// For now, orgOverride is treated as a path.
func resolveOrgByName(override string) (string, error) {
	abs, err := filepath.Abs(override)
	if err != nil {
		return "", fmt.Errorf("cannot resolve org path: %w", err)
	}
	orgDir := filepath.Join(abs, OrgDirName)
	if info, err := os.Stat(orgDir); err == nil && info.IsDir() {
		return orgDir, nil
	}
	return "", fmt.Errorf("no %s/ found at %s", OrgDirName, abs)
}

// resolveProjectByName searches under orgDir's parent for a project with matching name.
func resolveProjectByName(orgDir, name string) (string, error) {
	orgParent := filepath.Dir(orgDir)
	var found string
	filepath.Walk(orgParent, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Name() == "config.toml" && filepath.Base(filepath.Dir(path)) == ProjectDirName {
			cfg, loadErr := config.Load(filepath.Dir(path))
			if loadErr == nil && cfg.Project.Name == name {
				found = filepath.Dir(path)
				return filepath.SkipAll
			}
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("project %q not found under %s", name, orgParent)
	}
	return found, nil
}

// resolveRelPath resolves a path relative to base. Absolute paths returned as-is.
func resolveRelPath(base, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(filepath.Dir(base), rel)
}

func realPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return r
}

func mustGetwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("cannot get working directory: %v", err))
	}
	return cwd
}

func mustHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Sprintf("cannot determine home directory: %v", err))
	}
	return home
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/root/ -run 'TestFind' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/root/
git commit -m 'root: rewrite resolution for org/project split'
```

---

### Task 4: Rewrite Init Functions

**Files:**
- Rewrite: `internal/root/init.go`
- Create: `internal/root/init_test.go`

**Step 1: Write the failing test**

Create `internal/root/init_test.go`:

```go
package root

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
)

func TestInstallOrg(t *testing.T) {
	base := t.TempDir()

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	expected := filepath.Join(base, ".ateamorg")
	if orgDir != expected {
		t.Errorf("orgDir = %q, want %q", orgDir, expected)
	}

	// Check defaults were written
	if _, err := os.Stat(filepath.Join(orgDir, "defaults", "agents", "security", "report_prompt.md")); err != nil {
		t.Error("missing defaults/agents/security/report_prompt.md")
	}
	if _, err := os.Stat(filepath.Join(orgDir, "defaults", "supervisor", "review_prompt.md")); err != nil {
		t.Error("missing defaults/supervisor/review_prompt.md")
	}

	// Check empty agent dirs were created
	for _, id := range prompts.AllAgentIDs {
		dir := filepath.Join(orgDir, "agents", id)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("missing org agent dir: %s", dir)
		}
	}

	// Check supervisor dir
	if _, err := os.Stat(filepath.Join(orgDir, "agents", "supervisor")); err != nil {
		t.Error("missing agents/supervisor dir")
	}
}

func TestInstallOrgAlreadyExists(t *testing.T) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, ".ateamorg"), 0755)

	_, err := InstallOrg(base)
	if err == nil {
		t.Fatal("expected error when .ateamorg already exists")
	}
}

func TestInitProject(t *testing.T) {
	base := t.TempDir()

	// Create org first
	orgDir, _ := InstallOrg(base)

	projPath := filepath.Join(base, "myproj")
	os.MkdirAll(projPath, 0755)

	opts := InitProjectOpts{
		Name:            "myproj",
		Source:          "myproj",
		GitRepo:         ".",
		GitRemoteOrigin: "https://example.com/myproj.git",
		EnabledAgents:   []string{"security", "testing_basic"},
		AllAgents:       prompts.AllAgentIDs,
	}

	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	expected := filepath.Join(projPath, ".ateam")
	if projDir != expected {
		t.Errorf("projDir = %q, want %q", projDir, expected)
	}

	// Load and verify config
	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	if cfg.Project.Name != "myproj" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
	if cfg.Agents["security"] != "enabled" {
		t.Errorf("Agents[security] = %q", cfg.Agents["security"])
	}
	if cfg.Agents["automation"] != "disabled" {
		t.Errorf("Agents[automation] = %q, want disabled", cfg.Agents["automation"])
	}

	// Check dirs
	if _, err := os.Stat(filepath.Join(projDir, "supervisor")); err != nil {
		t.Error("missing supervisor dir")
	}
}

func TestInitProjectAlreadyExists(t *testing.T) {
	base := t.TempDir()
	orgDir, _ := InstallOrg(base)

	projPath := filepath.Join(base, "myproj")
	os.MkdirAll(filepath.Join(projPath, ".ateam"), 0755)

	opts := InitProjectOpts{Name: "myproj"}
	_, err := InitProject(projPath, orgDir, opts)
	if err == nil {
		t.Fatal("expected error when .ateam already exists")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/root/ -run 'TestInstall|TestInit' -v`
Expected: FAIL

**Step 3: Rewrite init.go**

Replace `internal/root/init.go` with:

```go
package root

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
)

// InstallOrg creates a new .ateamorg/ directory at parentDir with defaults and empty agent dirs.
func InstallOrg(parentDir string) (string, error) {
	orgDir := filepath.Join(parentDir, OrgDirName)

	if _, err := os.Stat(orgDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", OrgDirName, parentDir)
	}

	// Create empty agent override dirs
	for _, id := range prompts.AllAgentIDs {
		if err := os.MkdirAll(filepath.Join(orgDir, "agents", id), 0755); err != nil {
			return "", fmt.Errorf("cannot create agent dir: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Join(orgDir, "agents", "supervisor"), 0755); err != nil {
		return "", fmt.Errorf("cannot create supervisor dir: %w", err)
	}

	// Write embedded defaults
	if err := prompts.WriteOrgDefaults(orgDir, false); err != nil {
		return "", err
	}

	return orgDir, nil
}

// InitProjectOpts configures project initialization.
type InitProjectOpts struct {
	Name            string
	Source          string
	GitRepo         string
	GitRemoteOrigin string
	EnabledAgents   []string
	AllAgents       []string
}

// InitProject creates a new .ateam/ directory at path with config.toml.
func InitProject(path, orgDir string, opts InitProjectOpts) (string, error) {
	projDir := filepath.Join(path, ProjectDirName)

	if _, err := os.Stat(projDir); err == nil {
		return "", fmt.Errorf("%s/ already exists at %s", ProjectDirName, path)
	}

	// Check for duplicate project name
	if opts.Name != "" {
		orgParent := filepath.Dir(orgDir)
		if dup := findProjectByName(orgParent, opts.Name); dup != "" {
			return "", fmt.Errorf("project %q already exists at %s", opts.Name, dup)
		}
	}

	// Create directories
	dirs := []string{
		projDir,
		filepath.Join(projDir, "supervisor", "history"),
	}
	allAgents := opts.AllAgents
	if len(allAgents) == 0 {
		allAgents = prompts.AllAgentIDs
	}
	for _, id := range allAgents {
		dirs = append(dirs, filepath.Join(projDir, "agents", id, "history"))
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("cannot create directory %s: %w", dir, err)
		}
	}

	// Build agents map
	agents := make(map[string]string)
	enabledSet := make(map[string]bool)
	for _, id := range opts.EnabledAgents {
		enabledSet[id] = true
	}
	for _, id := range allAgents {
		if enabledSet[id] {
			agents[id] = "enabled"
		} else {
			agents[id] = "disabled"
		}
	}

	cfg := config.Config{
		Project: config.ProjectConfig{Name: opts.Name, Source: opts.Source},
		Git:     config.GitConfig{Repo: opts.GitRepo, RemoteOriginURL: opts.GitRemoteOrigin},
		Report: config.ReportConfig{
			MaxParallel:              config.DefaultMaxParallel,
			AgentReportTimeoutMinutes: config.DefaultAgentReportTimeoutMinutes,
		},
		Agents: agents,
	}

	if err := config.Save(projDir, cfg); err != nil {
		return "", err
	}

	return projDir, nil
}

// EnsureAgents creates missing agent dirs under the project.
func EnsureAgents(projectDir string, agentIDs []string) error {
	for _, id := range agentIDs {
		if err := os.MkdirAll(filepath.Join(projectDir, "agents", id, "history"), 0755); err != nil {
			return fmt.Errorf("cannot create project agent directory: %w", err)
		}
	}
	return nil
}

// findProjectByName searches for a .ateam/config.toml with the given project name.
func findProjectByName(searchRoot, name string) string {
	var found string
	filepath.Walk(searchRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if info.Name() == "config.toml" && filepath.Base(filepath.Dir(path)) == ProjectDirName {
			cfg, loadErr := config.Load(filepath.Dir(path))
			if loadErr == nil && cfg.Project.Name == name {
				found = filepath.Dir(path)
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/root/ -run 'TestInstall|TestInit' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/root/
git commit -m 'root: rewrite init for org/project split'
```

---

### Task 5: 3-Level Prompt Fallback

**Files:**
- Modify: `internal/prompts/prompts.go`
- Modify: `internal/prompts/embed.go`
- Create: `internal/prompts/prompts_test.go`

**Step 1: Write the failing test**

Create `internal/prompts/prompts_test.go`:

```go
package prompts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWith3LevelFallback(t *testing.T) {
	base := t.TempDir()
	projectDir := filepath.Join(base, ".ateam")
	orgAgentsDir := filepath.Join(base, ".ateamorg", "agents")
	orgDefaultsDir := filepath.Join(base, ".ateamorg", "defaults")

	agentID := "security"

	// Create all three levels
	for _, dir := range []string{
		filepath.Join(projectDir, "agents", agentID),
		filepath.Join(orgAgentsDir, agentID),
		filepath.Join(orgDefaultsDir, "agents", agentID),
	} {
		os.MkdirAll(dir, 0755)
	}

	defaultContent := "default prompt"
	orgContent := "org override"
	projectContent := "project override"

	defaultPath := filepath.Join(orgDefaultsDir, "agents", agentID, ReportPromptFile)
	orgPath := filepath.Join(orgAgentsDir, agentID, ReportPromptFile)
	projectPath := filepath.Join(projectDir, "agents", agentID, ReportPromptFile)

	// Only defaults exist -> returns default
	os.WriteFile(defaultPath, []byte(defaultContent), 0644)
	got, err := readWith3LevelFallback(projectPath, orgPath, defaultPath, "test")
	if err != nil {
		t.Fatalf("defaults only: %v", err)
	}
	if got != defaultContent {
		t.Errorf("defaults only: got %q, want %q", got, defaultContent)
	}

	// Org override exists -> returns org
	os.WriteFile(orgPath, []byte(orgContent), 0644)
	got, err = readWith3LevelFallback(projectPath, orgPath, defaultPath, "test")
	if err != nil {
		t.Fatalf("org override: %v", err)
	}
	if got != orgContent {
		t.Errorf("org override: got %q, want %q", got, orgContent)
	}

	// Project override exists -> returns project
	os.WriteFile(projectPath, []byte(projectContent), 0644)
	got, err = readWith3LevelFallback(projectPath, orgPath, defaultPath, "test")
	if err != nil {
		t.Fatalf("project override: %v", err)
	}
	if got != projectContent {
		t.Errorf("project override: got %q, want %q", got, projectContent)
	}
}

func TestReadWith3LevelFallbackNoneExist(t *testing.T) {
	base := t.TempDir()
	_, err := readWith3LevelFallback(
		filepath.Join(base, "a"),
		filepath.Join(base, "b"),
		filepath.Join(base, "c"),
		"test",
	)
	if err == nil {
		t.Fatal("expected error when no files exist")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/prompts/ -run 'TestRead' -v`
Expected: FAIL — `readWith3LevelFallback` doesn't exist

**Step 3: Update prompts.go**

Add the 3-level fallback function and update `AssembleAgentPrompt` and `AssembleReviewPrompt` to accept `orgDir` parameter:

```go
// readWith3LevelFallback tries project, then org override, then org defaults.
func readWith3LevelFallback(projectPath, orgPath, defaultPath, label string) (string, error) {
	if data, err := os.ReadFile(projectPath); err == nil {
		return string(data), nil
	}
	if data, err := os.ReadFile(orgPath); err == nil {
		return string(data), nil
	}
	if data, err := os.ReadFile(defaultPath); err == nil {
		return string(data), nil
	}
	return "", fmt.Errorf("no prompt found for %s (checked %s, %s, and %s)", label, projectPath, orgPath, defaultPath)
}
```

Update `AssembleAgentPrompt` signature to take `orgDir` (path to `.ateamorg/`) instead of `ateamRoot`:

```go
func AssembleAgentPrompt(orgDir, projectDir, agentID, sourceDir, extraPrompt string, meta *gitutil.ProjectMeta) (string, error) {
	rolePrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "agents", agentID, ReportPromptFile),
		filepath.Join(orgDir, "agents", agentID, ReportPromptFile),
		filepath.Join(orgDir, "defaults", "agents", agentID, ReportPromptFile),
		"agent "+agentID,
	)
	if err != nil {
		return "", err
	}

	instructions := readFileOr(
		filepath.Join(orgDir, "defaults", ReportPromptFile),
		"",
	)
	// ... rest stays the same but uses orgDir instead of ateamRoot
}
```

Similarly update `AssembleReviewPrompt`:

```go
func AssembleReviewPrompt(orgDir, projectDir string, meta *gitutil.ProjectMeta, extraPrompt, customPrompt string) (string, error) {
	// ... DiscoverReports stays the same ...

	supervisorPrompt, err := readWith3LevelFallback(
		filepath.Join(projectDir, "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "agents", "supervisor", ReviewPromptFile),
		filepath.Join(orgDir, "defaults", "supervisor", ReviewPromptFile),
		"supervisor",
	)
	// ... rest stays the same but uses orgDir
}
```

Also update `WriteRootDefaults` to `WriteOrgDefaults` — this writes defaults to `.ateamorg/defaults/`:

```go
func WriteOrgDefaults(orgDir string, overwrite bool) error {
	// Same logic but writes to orgDir/defaults/
	// ... (same as WriteRootDefaults but path is orgDir not ateamRoot)
}
```

And `DiffRootDefaults` → `DiffOrgDefaults`:

```go
func DiffOrgDefaults(orgDir string) []PromptDiff {
	// Same but uses orgDir
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/prompts/ -run 'TestRead' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/prompts/
git commit -m 'prompts: implement 3-level fallback for org/project split'
```

---

### Task 6: Commands — install and init

**Files:**
- Rewrite: `cmd/install.go`
- Rewrite: `cmd/init.go`

**Step 1: Rewrite install.go**

```go
package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var installCmd = &cobra.Command{
	Use:   "install [PATH]",
	Short: "Create a .ateamorg/ directory with default prompts",
	Long: `Create a .ateamorg/ directory at PATH (defaults to current directory)
with default prompt files and empty agent override directories.

Example:
  ateam install              # creates .ateamorg/ in cwd
  ateam install ~/projects   # creates ~/projects/.ateamorg/`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInstall,
}

func runInstall(cmd *cobra.Command, args []string) error {
	parentDir := "."
	if len(args) > 0 {
		parentDir = args[0]
	}

	absDir, err := filepath.Abs(parentDir)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	orgDir, err := root.InstallOrg(absDir)
	if err != nil {
		return err
	}

	fmt.Printf("Created %s\n", orgDir)
	return nil
}
```

**Step 2: Rewrite init.go**

```go
package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var (
	initSource    string
	initGitRemote string
	initName      string
	initAgentList []string
)

var initCmd = &cobra.Command{
	Use:   "init [PATH]",
	Short: "Initialize an ATeam project",
	Long: `Create a .ateam/ directory at PATH (defaults to current directory)
with project configuration. Requires a valid .ateamorg/ to be discoverable.

Example:
  ateam init
  ateam init --name myproj --agent security,testing_basic
  ateam init --source ~/projects/myproj --git-remote https://example.com/myproj.git`,
	Args: cobra.MaximumNArgs(1),
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initSource, "source", "", "path to source code (defaults to PATH)")
	initCmd.Flags().StringVar(&initGitRemote, "git-remote", "", "git remote URL (auto-discovered if not set)")
	initCmd.Flags().StringVar(&initName, "name", "", "project name (defaults to relative path from org)")
	initCmd.Flags().StringSliceVar(&initAgentList, "agent", nil, "comma-separated list of agents to enable")
}

func runInit(cmd *cobra.Command, args []string) error {
	path := "."
	if len(args) > 0 {
		path = args[0]
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %w", err)
	}

	// Find org
	orgDir, err := root.FindOrg(absPath)
	if err != nil {
		return err
	}
	orgParent := filepath.Dir(orgDir)

	// Determine source
	source := absPath
	if initSource != "" {
		source, err = filepath.Abs(initSource)
		if err != nil {
			return fmt.Errorf("cannot resolve source path: %w", err)
		}
	}

	// Determine name
	name := initName
	if name == "" {
		rel, relErr := filepath.Rel(orgParent, absPath)
		if relErr != nil {
			return fmt.Errorf("cannot compute project name: %w", relErr)
		}
		name = rel
	}

	// Determine git info
	gitRepo := ""
	gitRemote := initGitRemote
	if source != "" {
		gitRoot, gitErr := findGitRoot(source)
		if gitErr == nil {
			rel, relErr := filepath.Rel(source, gitRoot)
			if relErr == nil {
				gitRepo = rel
			}
			if gitRemote == "" {
				gitRemote = getGitRemoteOrigin(gitRoot)
			}
		}
	}

	// Determine source relative path for config
	sourceRel := ""
	if source != "" {
		rel, relErr := filepath.Rel(orgParent, source)
		if relErr != nil || strings.HasPrefix(rel, "..") {
			sourceRel = source // absolute if outside org tree
		} else {
			sourceRel = rel
		}
	}

	// Determine git repo relative path for config
	gitRepoRel := ""
	if gitRepo != "" {
		// gitRepo is relative from source to git root
		// For config we want it relative from source
		gitRepoRel = gitRepo
	}

	// Determine enabled agents
	enabledAgents := initAgentList
	if len(enabledAgents) == 0 {
		enabledAgents = prompts.AllAgentIDs
	} else {
		for _, id := range enabledAgents {
			if !prompts.IsValidAgent(id) {
				return fmt.Errorf("unknown agent: %s\nValid agents: %s", id, strings.Join(prompts.AllAgentIDs, ", "))
			}
		}
	}

	opts := root.InitProjectOpts{
		Name:            name,
		Source:          sourceRel,
		GitRepo:         gitRepoRel,
		GitRemoteOrigin: gitRemote,
		EnabledAgents:   enabledAgents,
		AllAgents:       prompts.AllAgentIDs,
	}

	projDir, err := root.InitProject(absPath, orgDir, opts)
	if err != nil {
		return err
	}

	fmt.Printf("Project initialized: %s\n", name)
	fmt.Printf("  Config: %s/config.toml\n", projDir)
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  ateam report --agents all\n")
	fmt.Printf("  ateam review\n")
	return nil
}

func findGitRoot(dir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func getGitRemoteOrigin(gitRoot string) string {
	cmd := exec.Command("git", "config", "remote.origin.url")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
```

**Step 3: Build to verify**

Run: `make build`
Expected: Will fail — commands reference old types. That's fine, we fix in tasks 7-8.

**Step 4: Commit**

```bash
git add cmd/install.go cmd/init.go
git commit -m 'cmd: rewrite install and init for org/project split'
```

---

### Task 7: Commands — update and projects

**Files:**
- Create: `cmd/update.go`
- Create: `cmd/projects.go`
- Delete: `cmd/update_prompts.go`

**Step 1: Create update.go**

```go
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/ateam-poc/internal/prompts"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var (
	updateQuiet bool
	updateDiff  bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update default prompts to the version embedded in this binary",
	Long: `Compare on-disk prompts in .ateamorg/defaults/ with the defaults
embedded in the ateam binary and overwrite any that differ.

Shows diffs by default unless --quiet is specified.

Example:
  ateam update
  ateam update --quiet`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().BoolVarP(&updateQuiet, "quiet", "q", false, "suppress diff output")
	updateCmd.Flags().BoolVar(&updateDiff, "diff", false, "show diffs (default when not --quiet)")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()
	orgDir, err := root.FindOrg(cwd)
	if err != nil {
		return err
	}

	fmt.Printf("Binary built: %s\n\n", BuildTime)

	diffs := prompts.DiffOrgDefaults(orgDir)
	if len(diffs) == 0 {
		fmt.Println("All prompts are up to date.")
		return nil
	}

	fmt.Printf("Found %d prompt(s) to update:\n", len(diffs))
	for _, d := range diffs {
		fmt.Printf("  %-55s %s\n", d.RelPath, d.Status)
	}
	fmt.Println()

	showDiff := !updateQuiet
	if showDiff {
		for _, d := range diffs {
			if d.Status == "changed" {
				diskPath := filepath.Join(orgDir, d.RelPath)
				diffCmd := exec.Command("diff", "-u", diskPath, "/dev/stdin")
				// We'll show a simple before/after instead
				fmt.Printf("--- %s\n", d.RelPath)
			}
		}
	}

	if err := prompts.WriteOrgDefaults(orgDir, true); err != nil {
		return err
	}

	fmt.Println("Prompts updated.")
	return nil
}
```

**Step 2: Create projects.go**

```go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/root"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List all ATeam projects under the organization",
	RunE:  runProjects,
}

func runProjects(cmd *cobra.Command, args []string) error {
	cwd, _ := os.Getwd()
	orgDir, err := root.FindOrg(cwd)
	if err != nil {
		return err
	}
	orgParent := filepath.Dir(orgDir)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Project\tPath\tSource Dir\tGit Repo Dir\tGit Remote\n")

	filepath.Walk(orgParent, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Name() == "config.toml" && filepath.Base(filepath.Dir(path)) == root.ProjectDirName {
			projDir := filepath.Dir(path)
			cfg, loadErr := config.Load(projDir)
			if loadErr != nil {
				return nil
			}
			relPath, _ := filepath.Rel(orgParent, filepath.Dir(projDir))
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				cfg.Project.Name,
				relPath,
				cfg.Project.Source,
				cfg.Git.Repo,
				cfg.Git.RemoteOriginURL,
			)
		}
		return nil
	})
	w.Flush()
	return nil
}
```

**Step 3: Delete update_prompts.go**

```bash
rm cmd/update_prompts.go
```

**Step 4: Commit**

```bash
git add cmd/update.go cmd/projects.go
git rm cmd/update_prompts.go
git commit -m 'cmd: add update and projects commands, remove update-prompts'
```

---

### Task 8: Adapt env, report, review Commands

**Files:**
- Modify: `cmd/env.go`
- Modify: `cmd/report.go`
- Modify: `cmd/review.go`

**Step 1: Update env.go**

Replace `runEnv` to use `root.Lookup()` which now returns `*ResolvedEnv`:

```go
func runEnv(cmd *cobra.Command, args []string) error {
	env, err := root.Lookup()
	if err != nil {
		return err
	}

	fmt.Printf("Organization: %s\n", env.OrgDir)

	if env.ProjectDir == "" {
		fmt.Printf("Project:      (not initialized)\n")
		return nil
	}

	fmt.Printf("Project:      %s\n", env.ProjectName)
	fmt.Printf("Project dir:  %s\n", env.ProjectDir)
	if env.SourceDir != "" {
		fmt.Printf("Source:       %s\n", env.SourceDir)
	}
	if env.GitRepoDir != "" {
		fmt.Printf("Git repo:     %s\n", env.GitRepoDir)
	}
	if env.Config != nil && env.Config.Git.RemoteOriginURL != "" {
		fmt.Printf("Git remote:   %s\n", env.Config.Git.RemoteOriginURL)
	}

	if env.Config != nil {
		enabled := env.Config.EnabledAgents()
		if len(enabled) > 0 {
			fmt.Printf("Agents:       %s\n", strings.Join(enabled, ", "))
			fmt.Println()
			fmt.Println("Reports:")
			for _, agentID := range enabled {
				reportPath := filepath.Join(env.ProjectDir, "agents", agentID, prompts.FullReportFile)
				printFileAge(reportPath, agentID, env.ProjectDir)
			}
		}

		reviewPath := filepath.Join(env.ProjectDir, "supervisor", "review.md")
		if fi, err := os.Stat(reviewPath); err == nil {
			fmt.Println()
			fmt.Printf("Review:       %s\n", formatAge(fi.ModTime()))
		}
	}

	return nil
}
```

**Step 2: Update report.go**

Change `runReport` to use `root.Resolve(orgFlag, projectFlag)` returning `*ResolvedEnv`:

Key changes:
- `root.Resolve(agentIDs)` → `root.Resolve(orgFlag, projectFlag)`
- `proj.AteamRoot` → `env.OrgDir`
- `proj.ProjectDir` → `env.ProjectDir`
- `proj.SourceDir` → `env.SourceDir`
- `proj.Config.Execution.EffectiveTimeout(...)` → `env.Config.Report.EffectiveTimeout(...)`
- `proj.Config.Execution.MaxParallel` → `env.Config.Report.MaxParallel`
- `prompts.AssembleAgentPrompt(proj.AteamRoot, proj.ProjectDir, ...)` → `prompts.AssembleAgentPrompt(env.OrgDir, env.ProjectDir, ...)`
- `proj.AgentReportPath(...)` → `env.AgentReportPath(...)`
- `proj.AgentHistoryDir(...)` → `env.AgentHistoryDir(...)`

**Step 3: Update review.go**

Same pattern:
- `root.Resolve(nil)` → `root.Resolve(orgFlag, projectFlag)`
- `proj.AteamRoot` → `env.OrgDir`
- All other field references updated similarly
- `printReviewDryRun` parameter type: `*root.ResolvedProject` → `*root.ResolvedEnv`
- `prompts.AssembleReviewPrompt(proj.AteamRoot, ...)` → `prompts.AssembleReviewPrompt(env.OrgDir, ...)`

**Step 4: Build to verify**

Run: `make build`
Expected: May still fail if root.go not updated yet — proceed to Task 9

**Step 5: Commit**

```bash
git add cmd/env.go cmd/report.go cmd/review.go
git commit -m 'cmd: adapt env, report, review to use ResolvedEnv'
```

---

### Task 9: Global Flags and root.go Wiring

**Files:**
- Modify: `cmd/root.go`

**Step 1: Add global flags and register commands**

```go
package cmd

import (
	"github.com/spf13/cobra"
)

var (
	orgFlag     string
	projectFlag string
)

var rootCmd = &cobra.Command{
	Use:   "ateam",
	Short: "ATeam — background agents for code quality",
	Long:  "ATeam manages role-specific agents that analyze your codebase and produce actionable reports.",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&orgFlag, "org", "o", "", "organization name/path override")
	rootCmd.PersistentFlags().StringVarP(&projectFlag, "project", "p", "", "project name override")

	rootCmd.AddCommand(envCmd)
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(reviewCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(projectsCmd)
}
```

**Step 2: Build**

Run: `make build`
Expected: PASS — all compilation errors should be resolved

**Step 3: Commit**

```bash
git add cmd/root.go
git commit -m 'cmd: add global -o/-p flags, wire up new commands'
```

---

### Task 10: Integration Tests

**Files:**
- Create: `internal/root/integration_test.go`

**Step 1: Write integration tests covering spec examples**

```go
package root

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam-poc/internal/config"
	"github.com/ateam-poc/internal/prompts"
)

func TestIntegration_BasicProject(t *testing.T) {
	// Simulates: cd ~/projects/level1/myproj && ateam init
	base := t.TempDir()

	// Install org at base (like ~/projects)
	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	// Create project dir
	projPath := filepath.Join(base, "level1", "myproj")
	os.MkdirAll(projPath, 0755)

	opts := InitProjectOpts{
		Name:            "level1/myproj",
		Source:          "level1/myproj",
		GitRepo:         ".",
		GitRemoteOrigin: "https://foobar/myproj.git",
		EnabledAgents:   prompts.AllAgentIDs,
		AllAgents:       prompts.AllAgentIDs,
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// Verify config
	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Name != "level1/myproj" {
		t.Errorf("Name = %q", cfg.Project.Name)
	}
	if cfg.Project.Source != "level1/myproj" {
		t.Errorf("Source = %q", cfg.Project.Source)
	}
	if cfg.Git.Repo != "." {
		t.Errorf("Git.Repo = %q", cfg.Git.Repo)
	}
	if cfg.Git.RemoteOriginURL != "https://foobar/myproj.git" {
		t.Errorf("Git.RemoteOriginURL = %q", cfg.Git.RemoteOriginURL)
	}

	// Verify resolution
	foundOrg, err := FindOrg(projPath)
	if err != nil {
		t.Fatalf("FindOrg: %v", err)
	}
	if foundOrg != orgDir {
		t.Errorf("FindOrg = %q, want %q", foundOrg, orgDir)
	}

	foundProj, err := FindProject(projPath)
	if err != nil {
		t.Fatalf("FindProject: %v", err)
	}
	if foundProj != projDir {
		t.Errorf("FindProject = %q, want %q", foundProj, projDir)
	}
}

func TestIntegration_MonorepoSubdir(t *testing.T) {
	// Simulates: cd ~/projects/level1/myproj/subdir_abc && ateam init
	base := t.TempDir()

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	projPath := filepath.Join(base, "level1", "myproj", "subdir_abc")
	os.MkdirAll(projPath, 0755)

	opts := InitProjectOpts{
		Name:            "level1/myproj/subdir_abc",
		Source:          "level1/myproj/subdir_abc",
		GitRepo:         "level1/myproj",
		GitRemoteOrigin: "https://foobar/myproj.git",
		EnabledAgents:   []string{"security"},
		AllAgents:       prompts.AllAgentIDs,
	}
	projDir, err := InitProject(projPath, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Name != "level1/myproj/subdir_abc" {
		t.Errorf("Name = %q", cfg.Project.Name)
	}
	if cfg.Agents["security"] != "enabled" {
		t.Errorf("security = %q", cfg.Agents["security"])
	}
	if cfg.Agents["automation"] != "disabled" {
		t.Errorf("automation = %q", cfg.Agents["automation"])
	}

	// Resolution from a child dir
	childDir := filepath.Join(projPath, "src", "pkg")
	os.MkdirAll(childDir, 0755)
	foundProj, err := FindProject(childDir)
	if err != nil {
		t.Fatalf("FindProject from child: %v", err)
	}
	if foundProj != projDir {
		t.Errorf("FindProject = %q, want %q", foundProj, projDir)
	}
}

func TestIntegration_ExternalProject(t *testing.T) {
	// Simulates: cd ~/projects/ateam_projects && ateam init --name myproj --source ~/projects/level1/myproj
	base := t.TempDir()

	orgDir, err := InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}

	ateamProjects := filepath.Join(base, "ateam_projects")
	os.MkdirAll(ateamProjects, 0755)

	sourcePath := filepath.Join(base, "level1", "myproj")
	os.MkdirAll(sourcePath, 0755)

	opts := InitProjectOpts{
		Name:            "myproj",
		Source:          sourcePath, // absolute path outside org tree is fine
		GitRepo:         ".",
		GitRemoteOrigin: "https://foobar/myproj.git",
		EnabledAgents:   prompts.AllAgentIDs,
		AllAgents:       prompts.AllAgentIDs,
	}
	projDir, err := InitProject(ateamProjects, orgDir, opts)
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	cfg, err := config.Load(projDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Project.Name != "myproj" {
		t.Errorf("Name = %q", cfg.Project.Name)
	}
}

func TestIntegration_DuplicateProjectName(t *testing.T) {
	base := t.TempDir()
	orgDir, _ := InstallOrg(base)

	proj1 := filepath.Join(base, "proj1")
	os.MkdirAll(proj1, 0755)
	opts := InitProjectOpts{
		Name:      "myproj",
		AllAgents: prompts.AllAgentIDs,
	}
	_, err := InitProject(proj1, orgDir, opts)
	if err != nil {
		t.Fatalf("first init: %v", err)
	}

	proj2 := filepath.Join(base, "proj2")
	os.MkdirAll(proj2, 0755)
	_, err = InitProject(proj2, orgDir, opts)
	if err == nil {
		t.Fatal("expected error for duplicate project name")
	}
}

func TestIntegration_MultipleProjects(t *testing.T) {
	base := t.TempDir()
	orgDir, _ := InstallOrg(base)

	for _, name := range []string{"frontend", "backend", "shared"} {
		projPath := filepath.Join(base, name)
		os.MkdirAll(projPath, 0755)
		opts := InitProjectOpts{
			Name:          name,
			Source:        name,
			EnabledAgents: []string{"security"},
			AllAgents:     prompts.AllAgentIDs,
		}
		_, err := InitProject(projPath, orgDir, opts)
		if err != nil {
			t.Fatalf("InitProject %s: %v", name, err)
		}
	}

	// Verify each project is independently discoverable
	for _, name := range []string{"frontend", "backend", "shared"} {
		projPath := filepath.Join(base, name)
		projDir, err := FindProject(projPath)
		if err != nil {
			t.Errorf("FindProject(%s): %v", name, err)
			continue
		}
		cfg, err := config.Load(projDir)
		if err != nil {
			t.Errorf("Load(%s): %v", name, err)
			continue
		}
		if cfg.Project.Name != name {
			t.Errorf("project %s: Name = %q", name, cfg.Project.Name)
		}
	}
}
```

**Step 2: Run all tests**

Run: `go test ./internal/... -v`
Expected: PASS

**Step 3: Build final binary**

Run: `make build`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/root/integration_test.go
git commit -m 'tests: integration tests for org/project split'
```

---

### Task 11: Cleanup and Final Verification

**Step 1: Remove dead code**

- Delete the `findSourceGit` function from `resolve.go` if duplicated (now in `cmd/init.go` as `findGitRoot`)
- Remove `resolveOutside`, `resolveOutsideWithSourceGit`, `resolveInside`, `isInsideAteam`, `findAteamRoot`, `AutoInitProject`, `Install` from old code if still present
- Remove old `EnvInfo` struct
- Clean up any remaining references to `ateamRoot` (should be `orgDir`)
- Remove `ResolvedProject` type

**Step 2: Run full test suite**

Run: `go test ./... -v`
Expected: PASS (excluding test_projects which may have build issues)

**Step 3: Build**

Run: `make build`
Expected: PASS

**Step 4: Manual smoke test**

```bash
# From a temp directory:
mkdir /tmp/ateam-test && cd /tmp/ateam-test
./ateam install .
./ateam init --name testproj --git-remote https://example.com/test.git
./ateam env
./ateam projects
```

**Step 5: Commit**

```bash
git add -A
git commit -m 'cleanup: remove old ateam resolution code'
```
