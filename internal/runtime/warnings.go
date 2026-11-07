package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// modelDateSuffix matches the -YYYY-MM-DD suffix of dated model ids. Same
// rule as agent.NormalizeModel; duplicated because agent (via secret)
// imports this package.
var modelDateSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// ModelWarnings audits the model configuration of one agent (and the
// profile that selected it, if any) and returns one message per issue:
//
//   - args or agent_extra_args embed --model / -m / --effort /
//     model_reasoning_effort: the model and effort attributes should be
//     used so layers can override them and audits can see them.
//   - no model pinned: the agent CLI picks its own default.
//   - the pinned model's output price is above expensive_output_per_mtok.
//
// cliModel is the --model / --cheaper-model override for this run; it
// silences the unpinned warning and is the model checked for price.
// Agents of type "builtin" (mock) have no model and produce nothing.
func (c *Config) ModelWarnings(prof *ProfileConfig, ac *AgentConfig, cliModel string) []string {
	if ac == nil || ac.Type == "builtin" {
		return nil
	}
	var out []string
	label := fmt.Sprintf("agent %q", ac.Name)

	if tok := embeddedModelFlag(ac.Args); tok != "" {
		src, _ := c.Source("agent." + ac.Name + ".args")
		out = append(out, fmt.Sprintf("%s: args embed %s (%s); use the %s attribute instead so layers can override it%s",
			label, tok, c.displayLayer(src.Layer), attrForFlag(tok), staleDefaultsAdvice(src.Layer)))
	}
	if prof != nil {
		if tok := embeddedModelFlag(prof.AgentExtraArgs); tok != "" {
			src, _ := c.Source("profile." + prof.Name + ".agent_extra_args")
			out = append(out, fmt.Sprintf("profile %q: agent_extra_args embed %s (%s); define an agent with a %s attribute instead%s",
				prof.Name, tok, c.displayLayer(src.Layer), attrForFlag(tok), staleDefaultsAdvice(src.Layer)))
		}
	}

	model := cliModel
	if model == "" {
		model = ac.Model
	}
	if model == "" {
		model = argsModel(ac.Args)
	}
	if model == "" && prof != nil {
		model = argsModel(prof.AgentExtraArgs)
	}
	if model == "" {
		out = append(out, fmt.Sprintf("%s: no model pinned; the %s CLI will pick its own default (often the most expensive). Set model = \"...\" in %s",
			label, ac.Command, c.editHint(ac.Name)))
		return out
	}

	if c.ExpensiveOutputPerMTok != nil && *c.ExpensiveOutputPerMTok > 0 && ac.Pricing != nil {
		if mp, ok := c.modelPrice(ac, model); ok && mp.OutputPerMTok > *c.ExpensiveOutputPerMTok {
			by := "--model"
			if cliModel == "" {
				src, _ := c.Source("agent." + ac.Name + ".model")
				by = c.displayLayer(src.Layer)
			}
			out = append(out, fmt.Sprintf("%s: model %q costs $%.2f per million output tokens, above expensive_output_per_mtok = %g (pinned by %s)",
				label, model, mp.OutputPerMTok, *c.ExpensiveOutputPerMTok, by))
		}
	}
	return out
}

func (c *Config) modelPrice(ac *AgentConfig, model string) (ModelPricing, bool) {
	if mp, ok := ac.Pricing.Models[model]; ok {
		return mp, true
	}
	mp, ok := ac.Pricing.Models[modelDateSuffix.ReplaceAllString(model, "")]
	return mp, ok
}

// editHint names the file and block a user should edit to pin a model on
// the agent: the nearest layer that already touches the block, else the
// nearest layer file (which may not exist yet).
func (c *Config) editHint(agentName string) string {
	prefix := "agent." + agentName + "."
	rank := make(map[string]int, len(c.Layers))
	for i, l := range c.Layers {
		rank[l.Label] = i
	}
	best := 0
	for key, chain := range c.Sources {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		for _, contrib := range chain {
			if r, ok := rank[contrib.Layer]; ok && r > best {
				best = r
			}
		}
	}
	block := fmt.Sprintf("agent %q", agentName)
	if best == 0 && len(c.Layers) > 1 {
		last := c.Layers[len(c.Layers)-1]
		if !last.Found {
			return fmt.Sprintf("%s (create it) in an %s block", c.displayLayer(last.Label), block)
		}
		return fmt.Sprintf("%s in an %s block", c.displayLayer(last.Label), block)
	}
	return fmt.Sprintf("%s in the %s block", c.displayLayer(c.Layers[best].Label), block)
}

// staleDefaultsAdvice points at `ateam update` when the offending layer is
// the on-disk copy of the built-in defaults (orgDir/defaults/runtime.hcl),
// which is refreshed by that command rather than edited by hand.
func staleDefaultsAdvice(label string) string {
	if filepath.Base(filepath.Dir(label)) == "defaults" && filepath.Base(label) == "runtime.hcl" {
		return "; this file is a copy of the built-in defaults, run 'ateam update' to refresh it"
	}
	return ""
}

// displayLayer shortens a layer label for messages: paths under $HOME
// become ~/..., the built-in label and base labels pass through.
func (c *Config) displayLayer(label string) string {
	if label == "" {
		return BuiltinLayer
	}
	if !filepath.IsAbs(label) {
		return label
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if label == home {
			return "~"
		}
		if strings.HasPrefix(label, home+string(filepath.Separator)) {
			return "~" + label[len(home):]
		}
	}
	return label
}

// embeddedModelFlag returns the first model/effort flag token found in
// args, or "" when none is present.
func embeddedModelFlag(args []string) string {
	for _, a := range args {
		switch {
		case a == "--model" || a == "-m" || a == "--effort":
			return a
		case strings.HasPrefix(a, "--model="), strings.HasPrefix(a, "--effort="):
			return a[:strings.Index(a, "=")]
		case strings.HasPrefix(a, "model_reasoning_effort="):
			return "model_reasoning_effort"
		}
	}
	return ""
}

func attrForFlag(tok string) string {
	if tok == "--effort" || tok == "model_reasoning_effort" {
		return "effort"
	}
	return "model"
}

// argsModel returns the value of the last --model / -m flag in args.
func argsModel(args []string) string {
	model := ""
	for i, a := range args {
		switch {
		case (a == "--model" || a == "-m") && i+1 < len(args):
			model = args[i+1]
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		}
	}
	return model
}

// argsEffort returns the value of the last --effort flag (claude) or
// model_reasoning_effort= setting (codex, after -c) in args.
func argsEffort(args []string) string {
	effort := ""
	for i, a := range args {
		switch {
		case a == "--effort" && i+1 < len(args):
			effort = args[i+1]
		case strings.HasPrefix(a, "--effort="):
			effort = strings.TrimPrefix(a, "--effort=")
		case strings.HasPrefix(a, "model_reasoning_effort="):
			effort = strings.TrimPrefix(a, "model_reasoning_effort=")
		}
	}
	return effort
}
