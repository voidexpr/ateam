package runtime

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ateam/defaults"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// Config is the top-level runtime configuration parsed from runtime.hcl files.
type Config struct {
	Agents     map[string]AgentConfig
	Containers map[string]ContainerConfig
	Profiles   map[string]ProfileConfig
}

type AgentConfig struct {
	Name        string
	Base        string // inherit unset fields from this agent
	Command     string
	Args        []string
	Model       string
	Type        string            // "builtin" for mock, "codex", or "" for claude
	Env         map[string]string // env vars to set (empty string = unset from parent)
	Sandbox     string            // inline JSON settings template
	RWPaths     []string          // additional read-write paths merged into sandbox allowWrite
	ROPaths     []string          // additional read-only paths merged into sandbox additionalDirectories
	DeniedPaths []string          // paths merged into sandbox denyWrite
	ConfigDir   string            // sets CLAUDE_CONFIG_DIR; relative paths resolve from .ateam/, absolute used as-is
	Pricing     *AgentPricing     // cost estimation config (nil = no pricing)
	RequiredEnv []string          // env var names that must be set; "A|B" means at least one of A or B

	ArgsInsideContainer    []string // extra args when running inside a container (detected via /.dockerenv)
	ArgsOutsideContainer   []string // extra args when running on the host
	SandboxInsideContainer bool     // if false (default), skip sandbox settings inside containers
}

type AgentPricing struct {
	DefaultModel string
	Models       map[string]ModelPricing
}

type ModelPricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

type ContainerConfig struct {
	Name              string
	Type              string   // "none", "docker", "docker-exec"
	Dockerfile        string   // docker: relative to .ateam/ dir
	DockerContainer   string   // docker-exec: name of user-managed container
	ExecTemplate      string   // docker-exec: custom exec command (default: "docker exec {{CONTAINER}} {{CMD}}")
	ForwardEnv        []string // env var names to forward from host into container
	ExtraVolumes      []string // additional volume mounts, e.g. "../data:/data:ro"
	Precheck          string   // docker/docker-exec: precheck script path, relative to .ateam/ (or "" for convention default)
	CopyAteam         bool     // docker-exec: copy ateam binary into container via docker cp
	MountClaudeConfig bool     // docker: mount host ~/.claude/ read-only into container (needed for OAuth tokens)
}

type ProfileConfig struct {
	Name               string
	Agent              string   // references AgentConfig name
	Container          string   // references ContainerConfig name
	AgentExtraArgs     []string // appended to Runner.ExtraArgs
	ContainerExtraArgs []string // reserved for container launch args
}

// hclFile is the HCL schema for runtime.hcl.
type hclFile struct {
	Agents     []hclAgent     `hcl:"agent,block"`
	Containers []hclContainer `hcl:"container,block"`
	Profiles   []hclProfile   `hcl:"profile,block"`
	Remain     hcl.Body       `hcl:",remain"`
}

type hclAgent struct {
	Name                   string            `hcl:"name,label"`
	Base                   string            `hcl:"base,optional"`
	Command                string            `hcl:"command,optional"`
	Args                   []string          `hcl:"args,optional"`
	Model                  string            `hcl:"model,optional"`
	Type                   string            `hcl:"type,optional"`
	Env                    map[string]string `hcl:"env,optional"`
	Sandbox                string            `hcl:"sandbox,optional"`
	RWPaths                []string          `hcl:"rw_paths,optional"`
	ROPaths                []string          `hcl:"ro_paths,optional"`
	DeniedPaths            []string          `hcl:"denied_paths,optional"`
	ConfigDir              string            `hcl:"config_dir,optional"`
	RequiredEnv            []string          `hcl:"required_env,optional"`
	ArgsInsideContainer    []string          `hcl:"args_inside_container,optional"`
	ArgsOutsideContainer   []string          `hcl:"args_outside_container,optional"`
	SandboxInsideContainer bool              `hcl:"sandbox_inside_container,optional"`
	Pricing                []hclPricing      `hcl:"pricing,block"`
}

type hclPricing struct {
	DefaultModel string     `hcl:"default_model"`
	Models       []hclModel `hcl:"model,block"`
}

type hclModel struct {
	Name          string  `hcl:"name,label"`
	InputPerMTok  float64 `hcl:"input_per_mtok"`
	OutputPerMTok float64 `hcl:"output_per_mtok"`
}

type hclContainer struct {
	Name              string   `hcl:"name,label"`
	Type              string   `hcl:"type,optional"`
	Dockerfile        string   `hcl:"dockerfile,optional"`
	DockerContainer   string   `hcl:"docker_container,optional"`
	ExecTemplate      string   `hcl:"exec,optional"`
	ForwardEnv        []string `hcl:"forward_env,optional"`
	ExtraVolumes      []string `hcl:"extra_volumes,optional"`
	Precheck          string   `hcl:"precheck,optional"`
	CopyAteam         bool     `hcl:"copy_ateam,optional"`
	MountClaudeConfig bool     `hcl:"mount_claude_config,optional"`
}

type hclProfile struct {
	Name               string   `hcl:"name,label"`
	Agent              string   `hcl:"agent"`
	Container          string   `hcl:"container"`
	AgentExtraArgs     []string `hcl:"agent_extra_args,optional"`
	ContainerExtraArgs []string `hcl:"container_extra_args,optional"`
}

// Load reads runtime.hcl with 4-level resolution:
// embedded defaults -> orgDir/defaults/runtime.hcl -> orgDir/runtime.hcl -> projectDir/runtime.hcl
// Each level's blocks override (by name) those from the previous level.
func Load(projectDir, orgDir string) (*Config, error) {
	cfg := &Config{
		Agents:     make(map[string]AgentConfig),
		Containers: make(map[string]ContainerConfig),
		Profiles:   make(map[string]ProfileConfig),
	}

	// Level 1: embedded defaults
	defaultData, err := defaults.FS.ReadFile("runtime.hcl")
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded defaults: %w", err)
	}
	if err := mergeHCL(cfg, defaultData, "defaults/runtime.hcl"); err != nil {
		return nil, fmt.Errorf("cannot parse embedded defaults: %w", err)
	}

	if orgDir != "" {
		// Level 2: org defaults (e.g. .ateamorg/defaults/runtime.hcl)
		if err := mergeHCLFile(cfg, filepath.Join(orgDir, "defaults", "runtime.hcl")); err != nil {
			return nil, err
		}

		// Level 3: org override (e.g. .ateamorg/runtime.hcl)
		if err := mergeHCLFile(cfg, filepath.Join(orgDir, "runtime.hcl")); err != nil {
			return nil, err
		}
	}

	// Level 4: project override (e.g. .ateam/runtime.hcl)
	if projectDir != "" {
		if err := mergeHCLFile(cfg, filepath.Join(projectDir, "runtime.hcl")); err != nil {
			return nil, err
		}
	}

	// Resolve agent inheritance (base references)
	if err := cfg.resolveInheritance(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// resolveInheritance resolves base references in agent configs.
// Fields that are zero-valued in the child are inherited from the base.
func (c *Config) resolveInheritance() error {
	resolved := make(map[string]bool)
	var resolve func(name string, visited map[string]bool) error

	resolve = func(name string, visited map[string]bool) error {
		if resolved[name] {
			return nil
		}
		if visited[name] {
			return fmt.Errorf("circular agent base reference: %s", name)
		}
		visited[name] = true

		ac, ok := c.Agents[name]
		if !ok {
			return fmt.Errorf("unknown agent %q", name)
		}
		if ac.Base == "" {
			resolved[name] = true
			return nil
		}

		// Resolve the base first
		if err := resolve(ac.Base, visited); err != nil {
			return err
		}
		base, ok := c.Agents[ac.Base]
		if !ok {
			return fmt.Errorf("agent %q references unknown base %q", name, ac.Base)
		}

		// Inherit zero-valued fields from base
		if ac.Command == "" {
			ac.Command = base.Command
		}
		if ac.Args == nil {
			ac.Args = base.Args
		}
		if ac.Model == "" {
			ac.Model = base.Model
		}
		if ac.Type == "" {
			ac.Type = base.Type
		}
		if ac.Env == nil {
			ac.Env = base.Env
		}
		if ac.Sandbox == "" {
			ac.Sandbox = base.Sandbox
		}
		if ac.RWPaths == nil {
			ac.RWPaths = base.RWPaths
		}
		if ac.ROPaths == nil {
			ac.ROPaths = base.ROPaths
		}
		if ac.DeniedPaths == nil {
			ac.DeniedPaths = base.DeniedPaths
		}
		if ac.ConfigDir == "" {
			ac.ConfigDir = base.ConfigDir
		}
		if ac.Pricing == nil {
			ac.Pricing = base.Pricing
		}
		if ac.RequiredEnv == nil {
			ac.RequiredEnv = base.RequiredEnv
		}
		if ac.ArgsInsideContainer == nil {
			ac.ArgsInsideContainer = base.ArgsInsideContainer
		}
		if ac.ArgsOutsideContainer == nil {
			ac.ArgsOutsideContainer = base.ArgsOutsideContainer
		}
		// SandboxInsideContainer: bool defaults to false, only inherit if base is true and child hasn't explicitly set it.
		// Since we can't distinguish "not set" from "set to false" for bools, we inherit if base is true.
		if !ac.SandboxInsideContainer && base.SandboxInsideContainer {
			ac.SandboxInsideContainer = base.SandboxInsideContainer
		}

		c.Agents[name] = ac
		resolved[name] = true
		return nil
	}

	for name := range c.Agents {
		if err := resolve(name, make(map[string]bool)); err != nil {
			return err
		}
	}
	return nil
}

// mergeHCLFile reads a file (following symlinks) and merges it into cfg.
// Returns nil if the file doesn't exist.
func mergeHCLFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	if err := mergeHCL(cfg, data, path); err != nil {
		return fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return nil
}

func mergeHCL(cfg *Config, data []byte, filename string) error {
	// Pass 1: extract locals for expression evaluation
	evalCtx, err := parseLocals(data, filename)
	if err != nil {
		return err
	}

	// Pass 2: decode config with locals context
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(data, filename)
	if diags.HasErrors() {
		return diags
	}

	var hf hclFile
	diags = gohcl.DecodeBody(file.Body, evalCtx, &hf)
	if diags.HasErrors() {
		return diags
	}

	for _, a := range hf.Agents {
		var pricing *AgentPricing
		if len(a.Pricing) > 0 {
			hp := a.Pricing[0]
			models := make(map[string]ModelPricing, len(hp.Models))
			for _, m := range hp.Models {
				models[m.Name] = ModelPricing{
					InputPerMTok:  m.InputPerMTok,
					OutputPerMTok: m.OutputPerMTok,
				}
			}
			pricing = &AgentPricing{
				DefaultModel: hp.DefaultModel,
				Models:       models,
			}
		}
		cfg.Agents[a.Name] = AgentConfig{
			Name:                   a.Name,
			Base:                   a.Base,
			Command:                a.Command,
			Args:                   a.Args,
			Model:                  a.Model,
			Type:                   a.Type,
			Env:                    a.Env,
			Sandbox:                a.Sandbox,
			RWPaths:                a.RWPaths,
			ROPaths:                a.ROPaths,
			DeniedPaths:            a.DeniedPaths,
			ConfigDir:              a.ConfigDir,
			Pricing:                pricing,
			RequiredEnv:            a.RequiredEnv,
			ArgsInsideContainer:    a.ArgsInsideContainer,
			ArgsOutsideContainer:   a.ArgsOutsideContainer,
			SandboxInsideContainer: a.SandboxInsideContainer,
		}
	}
	for _, c := range hf.Containers {
		cfg.Containers[c.Name] = ContainerConfig(c)
	}
	for _, p := range hf.Profiles {
		cfg.Profiles[p.Name] = ProfileConfig(p)
	}
	return nil
}

// parseLocals extracts locals blocks from HCL data and builds an eval context.
// Locals are scoped to the file they're defined in.
func parseLocals(data []byte, filename string) (*hcl.EvalContext, error) {
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(data, filename)
	if diags.HasErrors() {
		return nil, diags
	}

	type localsFile struct {
		Locals []struct {
			Remain hcl.Body `hcl:",remain"`
		} `hcl:"locals,block"`
		Remain hcl.Body `hcl:",remain"`
	}

	var lf localsFile
	diags = gohcl.DecodeBody(file.Body, nil, &lf)
	if diags.HasErrors() {
		return nil, diags
	}

	if len(lf.Locals) == 0 {
		return nil, nil
	}

	locals := make(map[string]cty.Value)
	for _, lb := range lf.Locals {
		attrs, diags := lb.Remain.JustAttributes()
		if diags.HasErrors() {
			return nil, diags
		}
		for name, attr := range attrs {
			val, diags := attr.Expr.Value(nil)
			if diags.HasErrors() {
				return nil, fmt.Errorf("cannot evaluate local %q in %s: %s", name, filename, diags.Error())
			}
			locals[name] = val
		}
	}

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"local": cty.ObjectVal(locals),
		},
	}, nil
}

// ResolveDockerfile finds the Dockerfile using a fallback chain:
//  1. .ateam/roles/<role>/Dockerfile (if roleID is set)
//  2. .ateam/Dockerfile
//  3. .ateamorg/Dockerfile
//  4. .ateamorg/defaults/Dockerfile
//  5. embedded default Dockerfile (written to a temp file)
//
// The filename searched for comes from the container config (cc.Dockerfile,
// typically just "Dockerfile"). Returns the absolute path to the first match.
func ResolveDockerfile(cc *ContainerConfig, projectDir, orgDir, roleID string) (dockerfilePath string, tmpDir string, err error) {
	name := cc.Dockerfile
	if name == "" {
		name = "Dockerfile"
	}

	// Build the search path
	var candidates []string
	if projectDir != "" {
		if roleID != "" {
			candidates = append(candidates, filepath.Join(projectDir, "roles", roleID, name))
		}
		candidates = append(candidates, filepath.Join(projectDir, name))
	}
	if orgDir != "" {
		candidates = append(candidates, filepath.Join(orgDir, name))
		candidates = append(candidates, filepath.Join(orgDir, "defaults", name))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, "", nil
		}
	}

	// Final fallback: embedded default
	data, err := defaults.FS.ReadFile("Dockerfile")
	if err != nil {
		return "", "", fmt.Errorf("no Dockerfile found and embedded default missing: %w", err)
	}
	tmpDir, err = os.MkdirTemp("", "ateam-dockerfile-*")
	if err != nil {
		return "", "", fmt.Errorf("cannot create temp dir for Dockerfile: %w", err)
	}
	tmpFile := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		os.RemoveAll(tmpDir)
		return "", "", fmt.Errorf("cannot write embedded Dockerfile: %w", err)
	}
	return tmpFile, tmpDir, nil
}

// ResolvePrecheckScript finds the precheck script using a fallback chain:
//  1. Explicit cc.Precheck (relative to projectDir)
//  2. .ateam/roles/<role>/docker-agent-precheck.sh (if roleID is set)
//  3. .ateam/docker-agent-precheck.sh
//  4. .ateamorg/docker-agent-precheck.sh
//  5. .ateamorg/defaults/docker-agent-precheck.sh
//
// Returns the absolute path to the first match, or "" if none found.
// Unlike ResolveDockerfile, there is no embedded fallback — if no script exists,
// the precheck is simply skipped.
func ResolvePrecheckScript(cc *ContainerConfig, projectDir, orgDir, roleID string) string {
	if cc.Precheck != "" {
		path := cc.Precheck
		if !filepath.IsAbs(path) && projectDir != "" {
			path = filepath.Join(projectDir, path)
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
		return ""
	}

	const name = "docker-agent-precheck.sh"
	var candidates []string
	if projectDir != "" {
		if roleID != "" {
			candidates = append(candidates, filepath.Join(projectDir, "roles", roleID, name))
		}
		candidates = append(candidates, filepath.Join(projectDir, name))
	}
	if orgDir != "" {
		candidates = append(candidates, filepath.Join(orgDir, name))
		candidates = append(candidates, filepath.Join(orgDir, "defaults", name))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// WriteOrgDefaults writes the embedded runtime.hcl and Dockerfile to orgDir/defaults/.
// When overwrite is false, existing files are not overwritten.
func WriteOrgDefaults(orgDir string, overwrite bool) error {
	defaultsDir := filepath.Join(orgDir, "defaults")
	if err := os.MkdirAll(defaultsDir, 0755); err != nil {
		return fmt.Errorf("cannot create defaults directory: %w", err)
	}
	for _, name := range []string{"runtime.hcl", "Dockerfile"} {
		dst := filepath.Join(defaultsDir, name)
		if !overwrite {
			if _, err := os.Stat(dst); err == nil {
				continue
			}
		}
		data, err := defaults.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("cannot read embedded %s: %w", name, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("cannot write %s: %w", dst, err)
		}
	}
	return nil
}

// ResolveProfile looks up a profile by name and validates agent/container refs.
func (c *Config) ResolveProfile(name string) (*ProfileConfig, *AgentConfig, *ContainerConfig, error) {
	prof, ok := c.Profiles[name]
	if !ok {
		return nil, nil, nil, fmt.Errorf("unknown profile %q", name)
	}
	ac, ok := c.Agents[prof.Agent]
	if !ok {
		return nil, nil, nil, fmt.Errorf("profile %q references unknown agent %q", name, prof.Agent)
	}
	cc, ok := c.Containers[prof.Container]
	if !ok {
		return nil, nil, nil, fmt.Errorf("profile %q references unknown container %q", name, prof.Container)
	}
	return &prof, &ac, &cc, nil
}
