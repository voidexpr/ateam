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
	Type    string // "builtin" for mock, "" for external command
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
	Name    string   `hcl:"name,label"`
	Command string   `hcl:"command,optional"`
	Args    []string `hcl:"args,optional"`
	Model   string   `hcl:"model,optional"`
	Type    string   `hcl:"type,optional"`
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

// Load reads runtime.hcl with 3-level resolution:
// defaults (embedded) -> orgDir/runtime.hcl -> projectDir/runtime.hcl
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

	// Level 2: org runtime.hcl
	if orgDir != "" {
		orgPath := filepath.Join(orgDir, "runtime.hcl")
		if data, err := os.ReadFile(orgPath); err == nil {
			if err := mergeHCL(cfg, data, orgPath); err != nil {
				return nil, fmt.Errorf("cannot parse %s: %w", orgPath, err)
			}
		}
	}

	// Level 3: project runtime.hcl
	if projectDir != "" {
		projPath := filepath.Join(projectDir, "runtime.hcl")
		if data, err := os.ReadFile(projPath); err == nil {
			if err := mergeHCL(cfg, data, projPath); err != nil {
				return nil, fmt.Errorf("cannot parse %s: %w", projPath, err)
			}
		}
	}

	return cfg, nil
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
