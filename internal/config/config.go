package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ateam/defaults"
	"github.com/BurntSushi/toml"
)

const (
	RoleEnabled  = "on"
	RoleDisabled = "off"
)

// DefaultConfig returns a Config populated from the embedded config.toml.
func DefaultConfig() Config {
	data, err := defaults.FS.ReadFile("config.toml")
	if err != nil {
		panic(fmt.Sprintf("cannot read embedded config.toml: %v", err))
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		panic(fmt.Sprintf("cannot parse embedded config.toml: %v", err))
	}
	return cfg
}

// Config represents the project's config.toml.
type Config struct {
	Project    ProjectConfig     `toml:"project"`
	Git        GitConfig         `toml:"git"`
	Report     ReportConfig      `toml:"report"`
	Review     ReviewConfig      `toml:"review"`
	Code       CodeConfig        `toml:"code"`
	Roles      map[string]string `toml:"roles"`
	Supervisor SupervisorConfig  `toml:"supervisor"`
	Profiles   ProfilesConfig    `toml:"profiles"`
}

type ProjectConfig struct {
	Name           string `toml:"name"`
	DefaultProfile string `toml:"default_profile"`
}

type SupervisorConfig struct {
	DefaultProfile       string `toml:"default_profile"`
	ReviewProfile        string `toml:"review_profile"`
	CodeProfile          string `toml:"code_profile"`
	CodeSupervisorProfile string `toml:"code_supervisor_profile"`
}

type ProfilesConfig struct {
	Roles map[string]string `toml:"roles"` // role -> profile name
}

type GitConfig struct {
	Repo            string `toml:"repo"`
	RemoteOriginURL string `toml:"remote_origin_url"`
}

type ReportConfig struct {
	MaxParallel          int `toml:"max_parallel"`
	ReportTimeoutMinutes int `toml:"report_timeout_minutes"`
}

type ReviewConfig struct {
	TimeoutMinutes int `toml:"timeout_minutes"`
}

func (r ReviewConfig) EffectiveTimeout(override int) int {
	if override > 0 {
		return override
	}
	return r.TimeoutMinutes
}

type CodeConfig struct {
	TimeoutMinutes int `toml:"timeout_minutes"`
}

func (c CodeConfig) EffectiveTimeout(override int) int {
	if override > 0 {
		return override
	}
	return c.TimeoutMinutes
}

// EffectiveMaxParallel returns the override if positive, otherwise the configured max_parallel.
func (r ReportConfig) EffectiveMaxParallel(override int) int {
	if override > 0 {
		return override
	}
	return r.MaxParallel
}

// EffectiveTimeout returns the override if positive, otherwise the configured timeout.
func (r ReportConfig) EffectiveTimeout(override int) int {
	if override > 0 {
		return override
	}
	return r.ReportTimeoutMinutes
}

// IsRoleEnabled returns true if status is "on" or "enabled" (backward compat).
func IsRoleEnabled(status string) bool {
	return status == "on" || status == "enabled"
}

// EnabledRoles returns a sorted slice of role names that are enabled.
func (c Config) EnabledRoles() []string {
	var enabled []string
	for name, status := range c.Roles {
		if IsRoleEnabled(status) {
			enabled = append(enabled, name)
		}
	}
	sort.Strings(enabled)
	return enabled
}

// ResolveProfile determines the profile name for a given action and role.
// Priority: role-specific profile > action-specific supervisor profile > project default > "default".
func (c Config) ResolveProfile(action, roleID string) string {
	if roleID != "" && c.Profiles.Roles != nil {
		if p, ok := c.Profiles.Roles[roleID]; ok && p != "" {
			return p
		}
	}
	switch action {
	case "review":
		if c.Supervisor.ReviewProfile != "" {
			return c.Supervisor.ReviewProfile
		}
	case "code":
		if c.Supervisor.CodeProfile != "" {
			return c.Supervisor.CodeProfile
		}
	}
	if c.Supervisor.DefaultProfile != "" {
		return c.Supervisor.DefaultProfile
	}
	if c.Project.DefaultProfile != "" {
		return c.Project.DefaultProfile
	}
	return "default"
}

// ResolveSupervisorProfile determines the profile for the supervisor itself.
// Priority: action-specific supervisor profile > supervisor default > project default > "default".
func (c Config) ResolveSupervisorProfile(action string) string {
	switch action {
	case "code":
		if c.Supervisor.CodeSupervisorProfile != "" {
			return c.Supervisor.CodeSupervisorProfile
		}
	}
	if c.Supervisor.DefaultProfile != "" {
		return c.Supervisor.DefaultProfile
	}
	if c.Project.DefaultProfile != "" {
		return c.Project.DefaultProfile
	}
	return "default"
}

// Load reads config.toml from the given directory.
// Missing fields are filled from the embedded default_config.toml.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read config.toml: %w (are you in an ateam project directory?)", err)
	}
	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config.toml: %w", err)
	}
	if cfg.Roles == nil {
		cfg.Roles = make(map[string]string)
	}
	return &cfg, nil
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
