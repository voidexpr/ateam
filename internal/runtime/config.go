// Package runtime handles runtime configuration parsing from HCL files and environment setup for agent execution.
package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ateam/defaults"
	"github.com/ateam/internal/container"
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

	// SandboxDetection toggles auto-detection of an outer sandbox
	// (fence, firejail, macOS Seatbelt, Linux bwrap-style). nil = use
	// the embedded default. Set via top-level scalar in runtime.hcl
	// (`sandbox_detection = true|false`). The last file in the load
	// order to set it explicitly wins. See
	// container.SandboxDetectionEnabled for the default and rationale.
	SandboxDetection *bool

	// DockerDetection toggles auto-detection of /.dockerenv and
	// /run/.containerenv. Same shape as SandboxDetection. See
	// container.DockerDetectionEnabled for the default and rationale.
	DockerDetection *bool

	// ExpensiveOutputPerMTok is the output price (USD per million tokens)
	// above which a pinned model draws a warning. nil = not set anywhere;
	// 0 or negative disables the warning. Top-level scalar
	// `expensive_output_per_mtok`, last file to set it wins.
	ExpensiveOutputPerMTok *float64

	// Layers lists every runtime.hcl source consulted by Load, lowest
	// precedence first, including files that did not exist.
	Layers []Layer

	// Sources records, per block attribute, every layer that set it, in
	// load order. Keys are "<block>.<name>.<attr>", e.g. "agent.claude.model",
	// "agent.claude.env.CLAUDECODE", "profile.default.agent". The last
	// contribution is the effective value. Values inherited through
	// `base` are recorded with the base agent as the layer.
	Sources map[string][]Contribution
}

// Layer is one runtime.hcl source in load order.
type Layer struct {
	Label string // "built-in" or the file path
	Found bool   // false when the file does not exist
}

// Contribution is what one layer set for one block attribute.
type Contribution struct {
	Layer string // Layer.Label, or `agent "<name>" (base)` for inherited values
	Value string // attribute value rendered for display
}

// Source returns the effective contribution for a Sources key.
func (c *Config) Source(key string) (Contribution, bool) {
	chain := c.Sources[key]
	if len(chain) == 0 {
		return Contribution{}, false
	}
	return chain[len(chain)-1], true
}

// BuiltinLayer is the label of the embedded defaults layer.
const BuiltinLayer = "built-in"

type AgentConfig struct {
	Name             string
	Base             string // inherit unset fields from this agent
	Command          string
	Args             []string
	Model            string
	Effort           string            // reasoning effort, passed verbatim to the agent CLI
	MaxBudgetUSD     string            // per-agent USD spend cap (claude native; codex unsupported)
	Type             string            // "builtin" for mock, "codex", or "" for claude
	StartTimeout     string            // codex-tmux duration string, e.g. "15s"
	BusyTimeout      string            // codex-tmux duration string, e.g. "20m"
	QuiescenceWindow string            // codex-tmux duration string, e.g. "2s"
	TmuxWidth        int               // codex-tmux pane width
	TmuxHeight       int               // codex-tmux pane height
	Env              map[string]string // env vars to set (empty string = unset from parent)
	Sandbox          string            // inline JSON settings template
	RWPaths          []string          // additional read-write paths merged into sandbox allowWrite
	ROPaths          []string          // additional read-only paths merged into sandbox additionalDirectories
	DeniedPaths      []string          // paths merged into sandbox denyWrite
	ConfigDir        string            // sets CLAUDE_CONFIG_DIR; relative paths resolve from .ateam/, absolute used as-is
	Pricing          *AgentPricing     // cost estimation config (nil = no pricing)
	RequiredEnv      []string          // env var names that must be set; "A|B" means at least one of A or B

	ArgsInsideContainer    []string // extra args when running inside a container (detected via /.dockerenv)
	ArgsOutsideContainer   []string // extra args when running on the host
	SandboxInsideContainer bool     // if false (default), skip sandbox settings inside containers
}

type AgentPricing struct {
	DefaultModel string
	Models       map[string]ModelPricing
}

type ModelPricing struct {
	InputPerMTok       float64
	CachedInputPerMTok float64
	OutputPerMTok      float64
}

type ContainerConfig struct {
	Name              string
	Type              string   // "none", "docker", "docker-exec"
	Dockerfile        string   // docker: relative to .ateam/ dir
	DockerContainer   string   // docker-exec: name of user-managed container
	ExecTemplate      string   // docker-exec: custom exec command (default: "docker exec {{CONTAINER}} {{CMD}}")
	ForwardEnv        []string // env var names to forward from host into container
	ExtraVolumes      []string // additional volume mounts, e.g. "../data:/data:ro"
	Precheck          []string // docker/docker-exec: precheck command, e.g. ["sh", "precheck.sh"] or ["make", "restart"]
	CopyAteam         bool     // docker-exec: copy ateam binary into container via docker cp
	MountClaudeConfig bool     // docker: mount host ~/.claude/ read-only into container (needed for OAuth tokens)
}

type ProfileConfig struct {
	Name               string
	Agent              string   // references AgentConfig name
	Container          string   // references ContainerConfig name
	AgentExtraArgs     []string // appended to AgentExecutor.ExtraArgs
	ContainerExtraArgs []string // reserved for container launch args
}

// hclFile is the HCL schema for runtime.hcl.
type hclFile struct {
	SandboxDetection *bool          `hcl:"sandbox_detection,optional"`
	DockerDetection  *bool          `hcl:"docker_detection,optional"`
	ExpensiveOutput  *float64       `hcl:"expensive_output_per_mtok,optional"`
	Agents           []hclAgent     `hcl:"agent,block"`
	Containers       []hclContainer `hcl:"container,block"`
	Profiles         []hclProfile   `hcl:"profile,block"`
	Remain           hcl.Body       `hcl:",remain"`
}

type hclAgent struct {
	Name                   string            `hcl:"name,label"`
	Body                   hcl.Body          `hcl:",body"`
	Base                   string            `hcl:"base,optional"`
	Command                string            `hcl:"command,optional"`
	Args                   []string          `hcl:"args,optional"`
	Model                  string            `hcl:"model,optional"`
	Effort                 string            `hcl:"effort,optional"`
	MaxBudgetUSD           string            `hcl:"max_budget_usd,optional"`
	Type                   string            `hcl:"type,optional"`
	StartTimeout           string            `hcl:"start_timeout,optional"`
	BusyTimeout            string            `hcl:"busy_timeout,optional"`
	QuiescenceWindow       string            `hcl:"quiescence_window,optional"`
	TmuxWidth              int               `hcl:"tmux_width,optional"`
	TmuxHeight             int               `hcl:"tmux_height,optional"`
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
	DefaultModel string     `hcl:"default_model,optional"`
	Models       []hclModel `hcl:"model,block"`
}

type hclModel struct {
	Name               string  `hcl:"name,label"`
	InputPerMTok       float64 `hcl:"input_per_mtok"`
	CachedInputPerMTok float64 `hcl:"cached_input_per_mtok,optional"`
	OutputPerMTok      float64 `hcl:"output_per_mtok"`
}

type hclContainer struct {
	Name              string   `hcl:"name,label"`
	Body              hcl.Body `hcl:",body"`
	Type              string   `hcl:"type,optional"`
	Dockerfile        string   `hcl:"dockerfile,optional"`
	DockerContainer   string   `hcl:"docker_container,optional"`
	ExecTemplate      string   `hcl:"exec,optional"`
	ForwardEnv        []string `hcl:"forward_env,optional"`
	ExtraVolumes      []string `hcl:"extra_volumes,optional"`
	Precheck          []string `hcl:"precheck,optional"`
	CopyAteam         bool     `hcl:"copy_ateam,optional"`
	MountClaudeConfig bool     `hcl:"mount_claude_config,optional"`
}

type hclProfile struct {
	Name               string   `hcl:"name,label"`
	Body               hcl.Body `hcl:",body"`
	Agent              string   `hcl:"agent,optional"`
	Container          string   `hcl:"container,optional"`
	AgentExtraArgs     []string `hcl:"agent_extra_args,optional"`
	ContainerExtraArgs []string `hcl:"container_extra_args,optional"`
}

// Body schemas used to detect which attributes a block actually wrote.
// gohcl decoding cannot tell an absent attribute from one set to its zero
// value, and per-field layering needs to.
var (
	agentSchema, _     = gohcl.ImpliedBodySchema(hclAgent{})
	containerSchema, _ = gohcl.ImpliedBodySchema(hclContainer{})
	profileSchema, _   = gohcl.ImpliedBodySchema(hclProfile{})
)

// Load reads runtime.hcl with 4-level resolution:
// embedded defaults -> orgDir/defaults/runtime.hcl -> orgDir/runtime.hcl -> projectDir/runtime.hcl
// A later level overrides only the attributes it sets on a same-name
// block; `env` maps merge per key, lists replace. Every value is recorded
// in Sources with the layer that set it.
func Load(projectDir, orgDir string) (*Config, error) {
	cfg := &Config{
		Agents:     make(map[string]AgentConfig),
		Containers: make(map[string]ContainerConfig),
		Profiles:   make(map[string]ProfileConfig),
		Sources:    make(map[string][]Contribution),
	}

	// Level 1: embedded defaults
	defaultData, err := defaults.FS.ReadFile("runtime.hcl")
	if err != nil {
		return nil, fmt.Errorf("cannot read embedded defaults: %w", err)
	}
	cfg.Layers = append(cfg.Layers, Layer{Label: BuiltinLayer, Found: true})
	if err := mergeHCL(cfg, defaultData, "defaults/runtime.hcl", BuiltinLayer); err != nil {
		return nil, fmt.Errorf("cannot parse embedded defaults: %w", err)
	}

	var files []string
	if orgDir != "" {
		// Level 2: org defaults (e.g. .ateamorg/defaults/runtime.hcl)
		// Level 3: org override (e.g. .ateamorg/runtime.hcl)
		files = append(files,
			filepath.Join(orgDir, "defaults", "runtime.hcl"),
			filepath.Join(orgDir, "runtime.hcl"))
	}
	if projectDir != "" {
		// Level 4: project override (e.g. .ateam/runtime.hcl)
		files = append(files, filepath.Join(projectDir, "runtime.hcl"))
	}
	for _, path := range files {
		if err := mergeHCLFile(cfg, path); err != nil {
			return nil, err
		}
	}

	// Resolve agent inheritance (base references)
	if err := cfg.resolveInheritance(); err != nil {
		return nil, err
	}
	if err := cfg.validateProfiles(); err != nil {
		return nil, err
	}

	// Apply detection settings to the container package — but only if
	// a CLI flag (or earlier caller) hasn't already locked them.
	if cfg.SandboxDetection != nil {
		container.SetSandboxDetectionIfUnset(*cfg.SandboxDetection)
	}
	if cfg.DockerDetection != nil {
		container.SetDockerDetectionIfUnset(*cfg.DockerDetection)
	}

	return cfg, nil
}

// resolveInheritance resolves base references in agent configs. An
// attribute no layer set on the child is inherited from the (already
// resolved) base, and recorded in Sources with the base as its layer.
// `env` inherits per key.
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

		childKey := "agent." + name + "."
		baseKey := "agent." + ac.Base + "."
		baseLabel := fmt.Sprintf("agent %q (base)", ac.Base)
		for _, attr := range agentAttrNames(agentSchema) {
			if attr == "base" || attr == "env" {
				continue
			}
			if _, set := c.Sources[childKey+attr]; set {
				continue
			}
			if _, inBase := c.Sources[baseKey+attr]; !inBase {
				continue
			}
			c.record(childKey+attr, baseLabel, copyAgentAttr(&ac, &base, attr))
		}
		for k, v := range base.Env {
			if _, set := c.Sources[childKey+"env."+k]; set {
				continue
			}
			c.setEnvKey(&ac, k, v, childKey, baseLabel)
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

// validateProfiles rejects profiles left without an agent or container
// after merging. The attributes are optional per layer so a project file
// can override just one of them on an existing profile.
func (c *Config) validateProfiles() error {
	for name, p := range c.Profiles {
		if p.Agent == "" || p.Container == "" {
			src, _ := c.Source("profile." + name + ".name")
			return fmt.Errorf("profile %q (%s) must set both agent and container", name, src.Layer)
		}
	}
	return nil
}

// agentAttrNames returns the attribute and nested-block names of a body
// schema in a stable order, so provenance is recorded deterministically.
func agentAttrNames(schema *hcl.BodySchema) []string {
	names := make([]string, 0, len(schema.Attributes)+len(schema.Blocks))
	for _, a := range schema.Attributes {
		names = append(names, a.Name)
	}
	for _, b := range schema.Blocks {
		names = append(names, b.Type)
	}
	sort.Strings(names)
	return names
}

// presentAttrs returns the attribute and nested-block names written in body.
func presentAttrs(body hcl.Body, schema *hcl.BodySchema) map[string]bool {
	present := make(map[string]bool)
	if body == nil {
		return present
	}
	content, _, _ := body.PartialContent(schema)
	for name := range content.Attributes {
		present[name] = true
	}
	for _, b := range content.Blocks {
		present[b.Type] = true
	}
	return present
}

func (c *Config) record(key, layer, value string) {
	c.Sources[key] = append(c.Sources[key], Contribution{Layer: layer, Value: value})
}

// setEnvKey sets one env key on ac (cloning the map so the base agent's
// map is never shared) and records it.
func (c *Config) setEnvKey(ac *AgentConfig, k, v, keyPrefix, label string) {
	env := make(map[string]string, len(ac.Env)+1)
	for ek, ev := range ac.Env {
		env[ek] = ev
	}
	env[k] = v
	ac.Env = env
	c.record(keyPrefix+"env."+k, label, v)
}

// copyAgentAttr copies one attribute from src to dst and returns its
// rendered value. "env" and "base" are handled by the callers.
func copyAgentAttr(dst, src *AgentConfig, attr string) string {
	switch attr {
	case "command":
		dst.Command = src.Command
		return src.Command
	case "args":
		dst.Args = src.Args
		return renderList(src.Args)
	case "model":
		dst.Model = src.Model
		return src.Model
	case "effort":
		dst.Effort = src.Effort
		return src.Effort
	case "max_budget_usd":
		dst.MaxBudgetUSD = src.MaxBudgetUSD
		return src.MaxBudgetUSD
	case "type":
		dst.Type = src.Type
		return src.Type
	case "start_timeout":
		dst.StartTimeout = src.StartTimeout
		return src.StartTimeout
	case "busy_timeout":
		dst.BusyTimeout = src.BusyTimeout
		return src.BusyTimeout
	case "quiescence_window":
		dst.QuiescenceWindow = src.QuiescenceWindow
		return src.QuiescenceWindow
	case "tmux_width":
		dst.TmuxWidth = src.TmuxWidth
		return strconv.Itoa(src.TmuxWidth)
	case "tmux_height":
		dst.TmuxHeight = src.TmuxHeight
		return strconv.Itoa(src.TmuxHeight)
	case "sandbox":
		dst.Sandbox = src.Sandbox
		return src.Sandbox
	case "rw_paths":
		dst.RWPaths = src.RWPaths
		return renderList(src.RWPaths)
	case "ro_paths":
		dst.ROPaths = src.ROPaths
		return renderList(src.ROPaths)
	case "denied_paths":
		dst.DeniedPaths = src.DeniedPaths
		return renderList(src.DeniedPaths)
	case "config_dir":
		dst.ConfigDir = src.ConfigDir
		return src.ConfigDir
	case "required_env":
		dst.RequiredEnv = src.RequiredEnv
		return renderList(src.RequiredEnv)
	case "args_inside_container":
		dst.ArgsInsideContainer = src.ArgsInsideContainer
		return renderList(src.ArgsInsideContainer)
	case "args_outside_container":
		dst.ArgsOutsideContainer = src.ArgsOutsideContainer
		return renderList(src.ArgsOutsideContainer)
	case "sandbox_inside_container":
		dst.SandboxInsideContainer = src.SandboxInsideContainer
		return strconv.FormatBool(src.SandboxInsideContainer)
	case "pricing":
		dst.Pricing = src.Pricing
		if src.Pricing == nil {
			return ""
		}
		return "default_model = " + src.Pricing.DefaultModel
	}
	return ""
}

func renderList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// mergeHCLFile reads a file (following symlinks) and merges it into cfg.
// Returns nil if the file doesn't exist. Emits a stderr warning when the
// file references ALL_CAPS template tokens the runner doesn't recognize
// — the prompt lifecycle refactor (Step 6) removed the engine-side
// compat shim so unknown ALL_CAPS pass through literally instead of
// being mapped to the dotted form; the warning catches typos early.
// Embedded defaults skip this check (they're shipped with the binary
// and trusted to be correct).
func mergeHCLFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg.Layers = append(cfg.Layers, Layer{Label: path})
			return nil
		}
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	cfg.Layers = append(cfg.Layers, Layer{Label: path, Found: true})
	warnStrayAllCaps(path, data)
	if err := mergeHCL(cfg, data, path, path); err != nil {
		return fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return nil
}

// mergeHCL merges one layer into cfg. filename names the source in parse
// diagnostics; label is the layer name recorded in cfg.Sources.
func mergeHCL(cfg *Config, data []byte, filename, label string) error {
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

	if hf.SandboxDetection != nil {
		v := *hf.SandboxDetection
		cfg.SandboxDetection = &v
	}
	if hf.DockerDetection != nil {
		v := *hf.DockerDetection
		cfg.DockerDetection = &v
	}
	if hf.ExpensiveOutput != nil {
		v := *hf.ExpensiveOutput
		cfg.ExpensiveOutputPerMTok = &v
		cfg.record("expensive_output_per_mtok", label, strconv.FormatFloat(v, 'f', -1, 64))
	}

	for _, a := range hf.Agents {
		cfg.mergeAgent(a, label)
	}
	for _, c := range hf.Containers {
		cfg.mergeContainer(c, label)
	}
	for _, p := range hf.Profiles {
		cfg.mergeProfile(p, label)
	}
	return nil
}

func agentFromHCL(a hclAgent) AgentConfig {
	var pricing *AgentPricing
	if len(a.Pricing) > 0 {
		hp := a.Pricing[0]
		models := make(map[string]ModelPricing, len(hp.Models))
		for _, m := range hp.Models {
			models[m.Name] = ModelPricing{
				InputPerMTok:       m.InputPerMTok,
				CachedInputPerMTok: m.CachedInputPerMTok,
				OutputPerMTok:      m.OutputPerMTok,
			}
		}
		pricing = &AgentPricing{
			DefaultModel: hp.DefaultModel,
			Models:       models,
		}
	}
	return AgentConfig{
		Name:                   a.Name,
		Base:                   a.Base,
		Command:                a.Command,
		Args:                   a.Args,
		Model:                  a.Model,
		Effort:                 a.Effort,
		MaxBudgetUSD:           a.MaxBudgetUSD,
		Type:                   a.Type,
		StartTimeout:           a.StartTimeout,
		BusyTimeout:            a.BusyTimeout,
		QuiescenceWindow:       a.QuiescenceWindow,
		TmuxWidth:              a.TmuxWidth,
		TmuxHeight:             a.TmuxHeight,
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

// mergeAgent applies the attributes block a wrote onto the agent of the
// same name from earlier layers (or a new one).
func (c *Config) mergeAgent(a hclAgent, label string) {
	src := agentFromHCL(a)
	ac := c.Agents[a.Name]
	ac.Name = a.Name
	key := "agent." + a.Name + "."
	if _, seen := c.Agents[a.Name]; !seen {
		c.record(key+"name", label, a.Name)
	}
	for attr := range presentAttrs(a.Body, agentSchema) {
		switch attr {
		case "base":
			ac.Base = src.Base
			c.record(key+"base", label, src.Base)
		case "env":
			for k, v := range src.Env {
				c.setEnvKey(&ac, k, v, key, label)
			}
		default:
			c.record(key+attr, label, copyAgentAttr(&ac, &src, attr))
		}
	}
	c.Agents[a.Name] = ac
}

func (c *Config) mergeContainer(h hclContainer, label string) {
	cc := c.Containers[h.Name]
	cc.Name = h.Name
	key := "container." + h.Name + "."
	if _, seen := c.Containers[h.Name]; !seen {
		c.record(key+"name", label, h.Name)
	}
	for attr := range presentAttrs(h.Body, containerSchema) {
		var value string
		switch attr {
		case "type":
			cc.Type = h.Type
			value = h.Type
		case "dockerfile":
			cc.Dockerfile = h.Dockerfile
			value = h.Dockerfile
		case "docker_container":
			cc.DockerContainer = h.DockerContainer
			value = h.DockerContainer
		case "exec":
			cc.ExecTemplate = h.ExecTemplate
			value = h.ExecTemplate
		case "forward_env":
			cc.ForwardEnv = h.ForwardEnv
			value = renderList(h.ForwardEnv)
		case "extra_volumes":
			cc.ExtraVolumes = h.ExtraVolumes
			value = renderList(h.ExtraVolumes)
		case "precheck":
			cc.Precheck = h.Precheck
			value = renderList(h.Precheck)
		case "copy_ateam":
			cc.CopyAteam = h.CopyAteam
			value = strconv.FormatBool(h.CopyAteam)
		case "mount_claude_config":
			cc.MountClaudeConfig = h.MountClaudeConfig
			value = strconv.FormatBool(h.MountClaudeConfig)
		}
		c.record(key+attr, label, value)
	}
	c.Containers[h.Name] = cc
}

func (c *Config) mergeProfile(h hclProfile, label string) {
	p := c.Profiles[h.Name]
	p.Name = h.Name
	key := "profile." + h.Name + "."
	if _, seen := c.Profiles[h.Name]; !seen {
		c.record(key+"name", label, h.Name)
	}
	for attr := range presentAttrs(h.Body, profileSchema) {
		var value string
		switch attr {
		case "agent":
			p.Agent = h.Agent
			value = h.Agent
		case "container":
			p.Container = h.Container
			value = h.Container
		case "agent_extra_args":
			p.AgentExtraArgs = h.AgentExtraArgs
			value = renderList(h.AgentExtraArgs)
		case "container_extra_args":
			p.ContainerExtraArgs = h.ContainerExtraArgs
			value = renderList(h.ContainerExtraArgs)
		}
		c.record(key+attr, label, value)
	}
	c.Profiles[h.Name] = p
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

// ResolvePrecheckCmd resolves the precheck command for a container config.
//
// When cc.Precheck is set (explicit), the first element is resolved relative
// to projectDir and the slice is returned as-is.
//
// When cc.Precheck is empty, the convention fallback chain is searched:
//  1. .ateam/roles/<role>/docker-agent-precheck.sh (if roleID is set)
//  2. .ateam/docker-agent-precheck.sh
//  3. .ateamorg/docker-agent-precheck.sh
//  4. .ateamorg/defaults/docker-agent-precheck.sh
//
// Convention-discovered scripts are wrapped as ["sh", "<path>", "{{CONTAINER_NAME}}"]
// so the container name is passed as $1 at execution time (after docker ps resolution).
//
// Returns nil if no precheck is configured or found.
func ResolvePrecheckCmd(cc *ContainerConfig, projectDir, orgDir, roleID string) []string {
	if len(cc.Precheck) > 0 {
		resolved := make([]string, len(cc.Precheck))
		copy(resolved, cc.Precheck)
		// Resolve the first element relative to projectDir when it looks like
		// a file path (contains a separator). Bare command names like "make"
		// or "sh" are left for PATH resolution.
		if strings.Contains(resolved[0], string(filepath.Separator)) && !filepath.IsAbs(resolved[0]) && projectDir != "" {
			resolved[0] = filepath.Join(projectDir, resolved[0])
		}
		return resolved
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
			return []string{"sh", path, "{{CONTAINER_NAME}}"}
		}
	}
	return nil
}

// RuntimeDiff describes a single embedded default file (runtime.hcl, Dockerfile)
// that differs from the on-disk copy under orgDir/defaults/.
type RuntimeDiff struct {
	RelPath string // path relative to orgDir, e.g. "defaults/runtime.hcl"
	Status  string // "missing" (no on-disk copy) or "changed" (content differs)
}

// DiffOrgDefaults compares on-disk defaults files (runtime.hcl, Dockerfile)
// against the embedded defaults and returns the files that differ.
// Whitespace-only differences are ignored to match promptdata.DiffOrgDefaults.
func DiffOrgDefaults(orgDir string) []RuntimeDiff {
	var diffs []RuntimeDiff
	for _, name := range []string{"runtime.hcl", "Dockerfile"} {
		rel := filepath.Join("defaults", name)
		diskPath := filepath.Join(orgDir, rel)
		data, err := os.ReadFile(diskPath)
		if err != nil {
			diffs = append(diffs, RuntimeDiff{RelPath: rel, Status: "missing"})
			continue
		}
		embedded, err := defaults.FS.ReadFile(name)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) != strings.TrimSpace(string(embedded)) {
			diffs = append(diffs, RuntimeDiff{RelPath: rel, Status: "changed"})
		}
	}
	return diffs
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
