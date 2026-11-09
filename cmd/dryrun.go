package cmd

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/container"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/secret"
)

// dryRunOpts configures what printDryRunInfo displays.
type dryRunOpts struct {
	RoleID string
	Action string
	Batch  string
	Prompt string // if non-empty, printed at the end (truncated)
}

// printDryRunInfo prints resolved execution details for a dry run.
// This is the shared core used by both `exec --dry-run` and `report --dry-run`.
// blocks are the profile/agent/container blocks r was built from (see
// buildRunner); nil skips the secrets section.
func printDryRunInfo(r *runner.AgentExecutor, env *root.ResolvedEnv, blocks *resolvedRun, opts dryRunOpts) {
	agentName := r.Agent.Name()
	var model string
	if mp, ok := r.Agent.(agent.ModelProvider); ok {
		model = agent.NormalizeModel(mp.ModelName())
	}
	tmplVars := runner.BuildTemplateVars(r, runner.RunOpts{
		RoleID: opts.RoleID,
		Action: opts.Action,
		Batch:  opts.Batch,
	}, time.Now(), 0, agentName, model)
	resolvedAgent := runner.ResolveAgentTemplateArgs(r.Agent, tmplVars)
	resolvedExtraArgs := runner.ResolveTemplateArgs(r.ExtraArgs, tmplVars)

	// Agent and profile
	fmt.Printf("Agent:     %s\n", agentName)
	if r.Profile != "" {
		fmt.Printf("Profile:   %s\n", r.Profile)
	}
	if r.ContainerType != "" && r.ContainerType != "none" {
		name := r.ContainerType
		resolvedName := runner.ResolveTemplateString(r.ContainerName, tmplVars)
		if resolvedName != "" {
			source := ""
			switch r.ContainerNameSource {
			case runner.ContainerNameSourceCLI:
				source = ", via --container-name"
			case runner.ContainerNameSourceSecret:
				source = ", via ateam secret"
			case runner.ContainerNameSourceEnv:
				source = ", via env"
			}
			name += " (" + resolvedName + source + ")"
		}
		fmt.Printf("Container: %s\n", name)
	}
	printModelResolution(r, env)
	fmt.Println()

	// The agent's args already reflect the run's context (its
	// inside_container / outside_container block was applied when the
	// runner was built); the sandbox settings file is passed when one is
	// configured for that context.
	fullArgs := make([]string, len(resolvedExtraArgs))
	copy(fullArgs, resolvedExtraArgs)
	if r.Sandbox.Settings != "" {
		fullArgs = append(fullArgs, "--settings", "<logs>/<exec_id>/settings.json")
	}

	// Resolved command
	cmd, args := resolvedAgent.DebugCommandArgs(fullArgs)
	fmt.Printf("Command:\n  %s %s\n", cmd, strings.Join(args, " "))
	fmt.Println()

	// CLAUDE_CONFIG_DIR — show the agent's config_dir if set, or the env var for claude agents
	configDir := display.ExpandHome(runner.ResolveTemplateString(r.ConfigDir, tmplVars))
	if configDir != "" {
		var configPath string
		if filepath.IsAbs(configDir) {
			configPath = configDir
		} else if r.ProjectDir != "" {
			configPath = filepath.Join(r.ProjectDir, configDir)
		} else {
			configPath = configDir
		}
		fmt.Printf("CLAUDE_CONFIG_DIR: %s (from agent config_dir)\n\n", configPath)
	} else if agentName == "claude" {
		if envDir, ok := os.LookupEnv("CLAUDE_CONFIG_DIR"); ok {
			fmt.Printf("CLAUDE_CONFIG_DIR: %s (from environment)\n\n", envDir)
		}
	}

	// Docker command
	if r.Container != nil {
		dockerOpts := container.RunOpts{WorkDir: r.SourceDir}
		dockerCmd := r.Container.DebugCommand(dockerOpts)
		if dockerCmd != "" {
			fmt.Printf("Docker:\n  %s\n\n", dockerCmd)
		}
	}

	// Secrets
	printDryRunSecrets(env, blocks)

	// Sandbox
	if r.Sandbox.Settings != "" {
		fmt.Println("Sandbox: configured (use --verbose for full JSON)")
	} else if r.Container != nil || container.IsInContainer() {
		fmt.Println("Sandbox: none (inside container)")
	}

	// Prompt (optional)
	if opts.Prompt != "" {
		fmt.Println("Prompt:")
		if len(opts.Prompt) > 500 {
			fmt.Printf("  %s...\n  (%d chars total)\n", opts.Prompt[:500], len(opts.Prompt))
		} else {
			fmt.Printf("  %s\n", opts.Prompt)
		}
	}
}

// logIsolationResults prints a clear message when ateam secret overrides env vars.
// warnMissingSecretsInContainer downgrades a ValidateSecrets failure to a
// stderr warning. Used only when ateam runs inside a container with no fresh
// container launch: the container may already be authenticated via regular
// auth (~/.claude/.credentials.json, ~/.codex/auth.json), so a missing
// required_env shouldn't be fatal. If we can already see a locally configured
// agent of the right type, suppress the warning entirely — the auth is there.
// If the agent genuinely has no usable auth it will fail loudly itself.
func warnMissingSecretsInContainer(err error, ac *runtime.AgentConfig) {
	if err == nil {
		return
	}
	if ac != nil && agent.HasLocalAgentAuth(ac.Type) {
		return
	}
	fmt.Fprintf(os.Stderr, "Warning: %v\n\n"+
		"Continuing anyway — if the agent is pre-configured inside this container "+
		"(e.g. ~/.claude/.credentials.json, ~/.codex/auth.json) it will use that auth.\n", err)
}

func logIsolationResults(w io.Writer, results []secret.IsolationResult) {
	for _, ir := range results {
		if len(ir.Stripped) == 0 {
			continue
		}
		src := ir.ActiveSource
		if src != "env" {
			src = "ateam secret (" + src + ")"
		}
		fmt.Fprintf(w, "Notice: use %s from %s, ignore %s from the environment\n",
			ir.ActiveKey, src, strings.Join(ir.Stripped, ", "))
	}
}

func printDryRunSecrets(env *root.ResolvedEnv, blocks *resolvedRun) {
	if blocks == nil {
		return
	}
	var forwardEnv []string
	if blocks.cc != nil {
		forwardEnv = blocks.cc.ForwardEnv
	}
	run := *blocks.ac
	run.Env = maps.Clone(blocks.ac.Env)
	ac := &run
	resolver := secretResolver(env, secret.DefaultBackend())

	// Run isolation on this copy so details reflect active/stripped status.
	isoResults := secret.IsolateCredentials(ac, resolver)
	details := secret.ResolveAllRequired(ac, forwardEnv, resolver)
	if len(details) == 0 {
		return
	}

	logIsolationResults(os.Stdout, isoResults)
	for _, ir := range isoResults {
		if len(ir.Stripped) > 0 {
			fmt.Println()
			break
		}
	}

	fmt.Println("Secrets (resolution: ateam secret store → env fallback):")
	for _, d := range details {
		switch {
		case !d.Found:
			fmt.Printf("  %-30s ✗ not found\n", d.Name)
		case d.Status == "stripped":
			fmt.Printf("  %-30s ✗ stripped  (found in %s but overridden by ateam secret)\n", d.Name, d.Source)
		case d.Status == "active":
			label := d.Source
			if d.Source != "env" {
				label = "ateam secret, " + d.Source
			}
			fmt.Printf("  %-30s ✓ active   %s (%s)\n", d.Name, d.Masked, label)
		default:
			fmt.Printf("  %-30s ✓ %s (%s, %s)\n", d.Name, d.Masked, d.Source, d.Backend)
		}
	}
	fmt.Println()

	// Show agent env overrides (excluding secrets already shown above).
	if ac.Env != nil {
		secretKeys := map[string]bool{}
		for _, d := range details {
			secretKeys[d.Name] = true
		}
		var setVars, unsetVars []string
		for k, v := range ac.Env {
			if secretKeys[k] {
				continue
			}
			if v == "" {
				unsetVars = append(unsetVars, k)
			} else {
				setVars = append(setVars, k)
			}
		}
		sort.Strings(setVars)
		sort.Strings(unsetVars)
		if len(setVars) > 0 || len(unsetVars) > 0 {
			fmt.Println("Agent env overrides:")
			for _, k := range setVars {
				fmt.Printf("  %-30s = %s\n", k, ac.Env[k])
			}
			for _, k := range unsetVars {
				fmt.Printf("  %-30s   (excluded from parent env)\n", k)
			}
			fmt.Println()
		}
	}
}
