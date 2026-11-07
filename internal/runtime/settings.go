package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ateam/internal/config"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// Settings blocks of ateam.hcl: what config.toml used to hold. Merged per
// attribute across layers like the agent blocks, and recorded in Sources
// under "project.<attr>", "role.<id>.<attr>", "supervisor.<attr>",
// "sandbox_extra.<attr>" and "container_extra.<attr>".

type hclProject struct {
	Body                 hcl.Body `hcl:",body"`
	Name                 string   `hcl:"name,optional"`
	DefaultProfile       string   `hcl:"default_profile,optional"`
	ReportMaxParallel    int      `hcl:"report_max_parallel,optional"`
	ReportTimeoutMinutes int      `hcl:"report_timeout_minutes,optional"`
	ReviewTimeoutMinutes int      `hcl:"review_timeout_minutes,optional"`
	CodeTimeoutMinutes   int      `hcl:"code_timeout_minutes,optional"`
	VerifyTimeoutMinutes int      `hcl:"verify_timeout_minutes,optional"`
	ExecTimeoutMinutes   int      `hcl:"exec_timeout_minutes,optional"`
	ServePort            int      `hcl:"serve_port,optional"`
}

type hclRole struct {
	Name    string   `hcl:"name,label"`
	Body    hcl.Body `hcl:",body"`
	Status  string   `hcl:"status,optional"`
	Profile string   `hcl:"profile,optional"`
}

type hclSupervisor struct {
	Body                  hcl.Body `hcl:",body"`
	DefaultProfile        string   `hcl:"default_profile,optional"`
	ReviewProfile         string   `hcl:"review_profile,optional"`
	CodeProfile           string   `hcl:"code_profile,optional"`
	CodeSupervisorProfile string   `hcl:"code_supervisor_profile,optional"`
}

type hclSandboxExtra struct {
	Body                hcl.Body `hcl:",body"`
	AllowWrite          []string `hcl:"allow_write,optional"`
	AllowRead           []string `hcl:"allow_read,optional"`
	AllowDomains        []string `hcl:"allow_domains,optional"`
	UnsandboxedCommands []string `hcl:"unsandboxed_commands,optional"`
}

type hclContainerExtra struct {
	Body       hcl.Body          `hcl:",body"`
	ExtraArgs  []string          `hcl:"extra_args,optional"`
	ForwardEnv []string          `hcl:"forward_env,optional"`
	Env        map[string]string `hcl:"env,optional"`
}

var (
	projectSchema, _        = gohcl.ImpliedBodySchema(hclProject{})
	roleSchema, _           = gohcl.ImpliedBodySchema(hclRole{})
	supervisorSchema, _     = gohcl.ImpliedBodySchema(hclSupervisor{})
	sandboxExtraSchema, _   = gohcl.ImpliedBodySchema(hclSandboxExtra{})
	containerExtraSchema, _ = gohcl.ImpliedBodySchema(hclContainerExtra{})
)

func (c *Config) mergeProject(h hclProject, label string) {
	s := &c.Settings
	present, _ := presentAttrs(h.Body, projectSchema)
	for attr, line := range present {
		var value string
		switch attr {
		case "name":
			s.Project.Name = h.Name
			value = h.Name
		case "default_profile":
			s.Project.DefaultProfile = h.DefaultProfile
			value = h.DefaultProfile
		case "report_max_parallel":
			s.Report.MaxParallel = h.ReportMaxParallel
			value = strconv.Itoa(h.ReportMaxParallel)
		case "report_timeout_minutes":
			s.Report.ReportTimeoutMinutes = h.ReportTimeoutMinutes
			value = strconv.Itoa(h.ReportTimeoutMinutes)
		case "review_timeout_minutes":
			s.Review.TimeoutMinutes = h.ReviewTimeoutMinutes
			value = strconv.Itoa(h.ReviewTimeoutMinutes)
		case "code_timeout_minutes":
			s.Code.TimeoutMinutes = h.CodeTimeoutMinutes
			value = strconv.Itoa(h.CodeTimeoutMinutes)
		case "verify_timeout_minutes":
			s.Verify.TimeoutMinutes = h.VerifyTimeoutMinutes
			value = strconv.Itoa(h.VerifyTimeoutMinutes)
		case "exec_timeout_minutes":
			s.Exec.TimeoutMinutes = h.ExecTimeoutMinutes
			value = strconv.Itoa(h.ExecTimeoutMinutes)
		case "serve_port":
			s.Serve.Port = h.ServePort
			value = strconv.Itoa(h.ServePort)
		}
		c.record("project."+attr, label, value, line)
	}
}

func (c *Config) mergeRole(h hclRole, label string) {
	s := &c.Settings
	key := "role." + h.Name + "."
	present, _ := presentAttrs(h.Body, roleSchema)
	for attr, line := range present {
		switch attr {
		case "status":
			if s.Roles == nil {
				s.Roles = map[string]string{}
			}
			s.Roles[h.Name] = h.Status
			c.record(key+"status", label, h.Status, line)
		case "profile":
			if s.Profiles.Roles == nil {
				s.Profiles.Roles = map[string]string{}
			}
			s.Profiles.Roles[h.Name] = h.Profile
			c.record(key+"profile", label, h.Profile, line)
		}
	}
	if _, known := s.Roles[h.Name]; !known {
		// A role block that only sets a profile still declares the role.
		if s.Roles == nil {
			s.Roles = map[string]string{}
		}
		s.Roles[h.Name] = config.RoleDisabled
	}
}

func (c *Config) mergeSupervisor(h hclSupervisor, label string) {
	s := &c.Settings.Supervisor
	present, _ := presentAttrs(h.Body, supervisorSchema)
	for attr, line := range present {
		var value string
		switch attr {
		case "default_profile":
			s.DefaultProfile = h.DefaultProfile
			value = h.DefaultProfile
		case "review_profile":
			s.ReviewProfile = h.ReviewProfile
			value = h.ReviewProfile
		case "code_profile":
			s.CodeProfile = h.CodeProfile
			value = h.CodeProfile
		case "code_supervisor_profile":
			s.CodeSupervisorProfile = h.CodeSupervisorProfile
			value = h.CodeSupervisorProfile
		}
		c.record("supervisor."+attr, label, value, line)
	}
}

func (c *Config) mergeSandboxExtra(h hclSandboxExtra, label string) {
	s := &c.Settings.SandboxExtra
	present, _ := presentAttrs(h.Body, sandboxExtraSchema)
	for attr, line := range present {
		var value []string
		switch attr {
		case "allow_write":
			s.AllowWrite = h.AllowWrite
			value = h.AllowWrite
		case "allow_read":
			s.AllowRead = h.AllowRead
			value = h.AllowRead
		case "allow_domains":
			s.AllowDomains = h.AllowDomains
			value = h.AllowDomains
		case "unsandboxed_commands":
			s.UnsandboxedCommands = h.UnsandboxedCommands
			value = h.UnsandboxedCommands
		}
		c.record("sandbox_extra."+attr, label, renderList(value), line)
	}
}

func (c *Config) mergeContainerExtra(h hclContainerExtra, label string) {
	s := &c.Settings.ContainerExtra
	present, attrs := presentAttrs(h.Body, containerExtraSchema)
	for attr, line := range present {
		switch attr {
		case "extra_args":
			s.ExtraArgs = h.ExtraArgs
			c.record("container_extra.extra_args", label, renderList(h.ExtraArgs), line)
		case "forward_env":
			s.ForwardEnv = h.ForwardEnv
			c.record("container_extra.forward_env", label, renderList(h.ForwardEnv), line)
		case "env":
			env := make(map[string]string, len(s.Env)+len(h.Env))
			for k, v := range s.Env {
				env[k] = v
			}
			keyLines := mapKeyLines(attrs["env"], h.Env)
			for k, v := range h.Env {
				env[k] = v
				c.record("container_extra.env."+k, label, v, keyLines[k])
			}
			s.Env = env
		}
	}
}

// ProjectName returns the `project { name = ... }` of the ateam.hcl in
// dir, or "" when the file is missing, unparsable, or names no project.
// Used by discovery to tell a project layer from a config-only layer
// without loading the whole configuration.
func ProjectName(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ConfigFile))
	if err != nil {
		return ""
	}
	file, diags := hclparse.NewParser().ParseHCL(data, ConfigFile)
	if diags.HasErrors() {
		return ""
	}
	var probe struct {
		Projects []struct {
			Name   string   `hcl:"name,optional"`
			Remain hcl.Body `hcl:",remain"`
		} `hcl:"project,block"`
		Remain hcl.Body `hcl:",remain"`
	}
	if diags := gohcl.DecodeBody(file.Body, nil, &probe); diags.HasErrors() {
		return ""
	}
	for _, p := range probe.Projects {
		if p.Name != "" {
			return p.Name
		}
	}
	return ""
}

// DefaultSettings returns the settings of the embedded defaults alone.
func DefaultSettings() config.Config {
	cfg, err := Load(nil)
	if err != nil {
		panic(fmt.Sprintf("embedded defaults do not load: %v", err))
	}
	return cfg.Settings
}
