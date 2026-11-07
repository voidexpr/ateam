// Package config holds the project settings read from the `project`,
// `role`, `supervisor`, `sandbox_extra` and `container_extra` blocks of
// ateam.hcl, and the profile selection rules built on them. Parsing and
// per-layer merging live in internal/runtime, next to the agent blocks.
package config

import (
	"fmt"
	"regexp"
	"sort"
)

const (
	RoleEnabled  = "on"
	RoleDisabled = "off"
)

// Config is the merged settings of every ateam.hcl layer.
type Config struct {
	Project        ProjectConfig
	Report         ReportConfig
	Review         ReviewConfig
	Code           CodeConfig
	Exec           ExecConfig
	Verify         VerifyConfig
	Serve          ServeConfig
	Roles          map[string]string // role id → "on" / "off"
	Supervisor     SupervisorConfig
	Profiles       ProfilesConfig
	SandboxExtra   SandboxExtraConfig
	ContainerExtra ContainerExtraConfig
}

type ServeConfig struct {
	Port int
}

type ProjectConfig struct {
	Name           string
	DefaultProfile string
}

type SupervisorConfig struct {
	DefaultProfile        string
	ReviewProfile         string
	CodeProfile           string
	CodeSupervisorProfile string
}

type ProfilesConfig struct {
	Roles map[string]string // role → profile name
}

type SandboxExtraConfig struct {
	AllowWrite          []string
	AllowRead           []string
	AllowDomains        []string
	UnsandboxedCommands []string
}

type ContainerExtraConfig struct {
	ExtraArgs  []string
	ForwardEnv []string
	Env        map[string]string
}

type ReportConfig struct {
	MaxParallel          int
	ReportTimeoutMinutes int
}

func effectiveInt(override, configured int) int {
	if override > 0 {
		return override
	}
	return configured
}

type ReviewConfig struct {
	TimeoutMinutes int
}

func (r ReviewConfig) EffectiveTimeout(override int) int {
	return effectiveInt(override, r.TimeoutMinutes)
}

type CodeConfig struct {
	TimeoutMinutes int
}

func (c CodeConfig) EffectiveTimeout(override int) int {
	return effectiveInt(override, c.TimeoutMinutes)
}

type ExecConfig struct {
	TimeoutMinutes int
}

func (e ExecConfig) EffectiveTimeout(override int) int {
	return effectiveInt(override, e.TimeoutMinutes)
}

type VerifyConfig struct {
	TimeoutMinutes int
}

func (v VerifyConfig) EffectiveTimeout(override int) int {
	return effectiveInt(override, v.TimeoutMinutes)
}

// EffectiveMaxParallel returns the override if positive, otherwise the configured max_parallel.
func (r ReportConfig) EffectiveMaxParallel(override int) int {
	return effectiveInt(override, r.MaxParallel)
}

// EffectiveTimeout returns the override if positive, otherwise the configured timeout.
func (r ReportConfig) EffectiveTimeout(override int) int {
	return effectiveInt(override, r.ReportTimeoutMinutes)
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

// ProfileChoice is a resolved profile name plus the ateam.hcl setting
// that chose it, so audits can explain why a role or action runs on a
// given profile.
type ProfileChoice struct {
	Name   string
	Source string // e.g. `role "project.security" profile`, "supervisor.code_profile", "built-in default"
}

// Sources for profile choices made outside ateam.hcl.
const (
	ProfileSourceFlag    = "--profile"
	ProfileSourceAgent   = "--agent"
	ProfileSourceBuiltin = "built-in default"
)

// ResolveProfile determines the profile for a given action and role.
// Priority: role-specific profile > action-specific supervisor profile > supervisor default > project default > "default".
func (c Config) ResolveProfile(action, roleID string) ProfileChoice {
	if roleID != "" && c.Profiles.Roles != nil {
		if p, ok := c.Profiles.Roles[roleID]; ok && p != "" {
			return ProfileChoice{p, fmt.Sprintf("role %q profile", roleID)}
		}
	}
	switch action {
	case "review":
		if c.Supervisor.ReviewProfile != "" {
			return ProfileChoice{c.Supervisor.ReviewProfile, "supervisor.review_profile"}
		}
	case "code":
		if c.Supervisor.CodeProfile != "" {
			return ProfileChoice{c.Supervisor.CodeProfile, "supervisor.code_profile"}
		}
	}
	return c.defaultProfile()
}

// ResolveSupervisorProfile determines the profile for the supervisor itself.
// Priority: action-specific supervisor profile > supervisor default > project default > "default".
func (c Config) ResolveSupervisorProfile(action string) ProfileChoice {
	switch action {
	case "code":
		if c.Supervisor.CodeSupervisorProfile != "" {
			return ProfileChoice{c.Supervisor.CodeSupervisorProfile, "supervisor.code_supervisor_profile"}
		}
	}
	return c.defaultProfile()
}

func (c Config) defaultProfile() ProfileChoice {
	if c.Supervisor.DefaultProfile != "" {
		return ProfileChoice{c.Supervisor.DefaultProfile, "supervisor.default_profile"}
	}
	if c.Project.DefaultProfile != "" {
		return ProfileChoice{c.Project.DefaultProfile, "project.default_profile"}
	}
	return ProfileChoice{"default", ProfileSourceBuiltin}
}

var projectNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidateProjectName enforces the project identity rules: lowercase
// letters, digits, dot, underscore and dash, starting with a letter or
// digit. The name is the project's id in the state db and the registry,
// so it must be safe as a path component and a slug.
func ValidateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name is empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("project name %q is longer than 64 characters", name)
	}
	if !projectNameRE.MatchString(name) {
		return fmt.Errorf("project name %q must match %s", name, projectNameRE)
	}
	return nil
}
