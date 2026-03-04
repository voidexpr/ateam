package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	DefaultMaxParallel              = 3
	DefaultAgentReportTimeoutMinutes = 10
)

// Config represents the project's config.toml.
type Config struct {
	Project   ProjectConfig   `toml:"project"`
	Agents    AgentsConfig    `toml:"agents"`
	Execution ExecutionConfig `toml:"execution"`
}

type ProjectConfig struct {
	Name      string `toml:"name"`
	SourceDir string `toml:"source_dir"`
}

type AgentsConfig struct {
	Enabled []string `toml:"enabled"`
}

type ExecutionConfig struct {
	MaxParallel              int `toml:"max_parallel"`
	AgentReportTimeoutMinutes int `toml:"agent_report_timeout_minutes"`
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig(name, sourceDir string, agents []string) Config {
	return Config{
		Project: ProjectConfig{
			Name:      name,
			SourceDir: sourceDir,
		},
		Agents: AgentsConfig{
			Enabled: agents,
		},
		Execution: ExecutionConfig{
			MaxParallel:              DefaultMaxParallel,
			AgentReportTimeoutMinutes: DefaultAgentReportTimeoutMinutes,
		},
	}
}

// Load reads config.toml from the given directory.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config.toml: %w (are you in an ateam project directory?)", err)
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config.toml: %w", err)
	}
	// Apply defaults for missing values
	if cfg.Execution.MaxParallel == 0 {
		cfg.Execution.MaxParallel = DefaultMaxParallel
	}
	if cfg.Execution.AgentReportTimeoutMinutes == 0 {
		cfg.Execution.AgentReportTimeoutMinutes = DefaultAgentReportTimeoutMinutes
	}
	return &cfg, nil
}

// EffectiveTimeout returns the override if positive, otherwise the configured timeout.
func (e ExecutionConfig) EffectiveTimeout(override int) int {
	if override > 0 {
		return override
	}
	return e.AgentReportTimeoutMinutes
}

// Save writes config.toml to the given directory.
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
