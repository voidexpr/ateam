package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

const (
	DefaultMaxParallel          = 3
	DefaultReportTimeoutMinutes = 20
	DefaultReviewTimeoutMinutes = 20
	DefaultCodeTimeoutMinutes   = 120

	RoleEnabled  = "enabled"
	RoleDisabled = "disabled"
)

// Config represents the project's config.toml.
type Config struct {
	Project ProjectConfig     `toml:"project"`
	Git     GitConfig         `toml:"git"`
	Report  ReportConfig      `toml:"report"`
	Review  ReviewConfig      `toml:"review"`
	Code    CodeConfig        `toml:"code"`
	Roles   map[string]string `toml:"roles"`
}

type ProjectConfig struct {
	Name string `toml:"name"`
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

// EffectiveTimeout returns the override if positive, otherwise the configured timeout.
func (r ReportConfig) EffectiveTimeout(override int) int {
	if override > 0 {
		return override
	}
	return r.ReportTimeoutMinutes
}

// EnabledRoles returns a sorted slice of role names that have "enabled" status.
func (c Config) EnabledRoles() []string {
	var enabled []string
	for name, status := range c.Roles {
		if status == RoleEnabled {
			enabled = append(enabled, name)
		}
	}
	sort.Strings(enabled)
	return enabled
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
	if cfg.Report.MaxParallel == 0 {
		cfg.Report.MaxParallel = DefaultMaxParallel
	}
	if cfg.Report.ReportTimeoutMinutes == 0 {
		cfg.Report.ReportTimeoutMinutes = DefaultReportTimeoutMinutes
	}
	if cfg.Review.TimeoutMinutes == 0 {
		cfg.Review.TimeoutMinutes = DefaultReviewTimeoutMinutes
	}
	if cfg.Code.TimeoutMinutes == 0 {
		cfg.Code.TimeoutMinutes = DefaultCodeTimeoutMinutes
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
