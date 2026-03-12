package runtime

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

//go:embed defaults/runtime.hcl
var defaultsFS embed.FS

// Config is the top-level runtime configuration parsed from runtime.hcl files.
type Config struct {
	Agents     map[string]AgentConfig
	Containers map[string]ContainerConfig
	Profiles   map[string]ProfileConfig
}

type AgentConfig struct {
	Name    string
	Command string
	Args    []string
	Model   string
	Type    string            // "builtin" for mock, "" for external command
	Env     map[string]string // env vars to set (empty string = unset from parent)
}

type ContainerConfig struct {
	Name string
	Type string // "none", "docker", "srt"
}

type ProfileConfig struct {
	Name      string
	Agent     string // references AgentConfig name
	Container string // references ContainerConfig name
}

// hclFile is the HCL schema for runtime.hcl.
type hclFile struct {
	Agents     []hclAgent     `hcl:"agent,block"`
	Containers []hclContainer `hcl:"container,block"`
	Profiles   []hclProfile   `hcl:"profile,block"`
	Remain     hcl.Body       `hcl:",remain"`
}

type hclAgent struct {
	Name    string            `hcl:"name,label"`
	Command string            `hcl:"command,optional"`
	Args    []string          `hcl:"args,optional"`
	Model   string            `hcl:"model,optional"`
	Type    string            `hcl:"type,optional"`
	Env     map[string]string `hcl:"env,optional"`
}

type hclContainer struct {
	Name string `hcl:"name,label"`
	Type string `hcl:"type,optional"`
}

type hclProfile struct {
	Name      string `hcl:"name,label"`
	Agent     string `hcl:"agent"`
	Container string `hcl:"container"`
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
	defaultData, err := defaultsFS.ReadFile("defaults/runtime.hcl")
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

	return cfg, nil
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
	parser := hclparse.NewParser()
	file, diags := parser.ParseHCL(data, filename)
	if diags.HasErrors() {
		return diags
	}

	var hf hclFile
	diags = gohcl.DecodeBody(file.Body, nil, &hf)
	if diags.HasErrors() {
		return diags
	}

	for _, a := range hf.Agents {
		cfg.Agents[a.Name] = AgentConfig{
			Name:    a.Name,
			Command: a.Command,
			Args:    a.Args,
			Model:   a.Model,
			Type:    a.Type,
			Env:     a.Env,
		}
	}
	for _, c := range hf.Containers {
		cfg.Containers[c.Name] = ContainerConfig{
			Name: c.Name,
			Type: c.Type,
		}
	}
	for _, p := range hf.Profiles {
		cfg.Profiles[p.Name] = ProfileConfig{
			Name:      p.Name,
			Agent:     p.Agent,
			Container: p.Container,
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
