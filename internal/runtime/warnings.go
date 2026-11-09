package runtime

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ateam/internal/display"
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
		out = append(out, fmt.Sprintf("%s: args embed %s (%s); use the %s attribute instead so layers can override it",
			label, tok, c.displayLayer(src.Layer), attrForFlag(tok)))
	}
	if prof != nil {
		if tok := embeddedModelFlag(prof.AgentExtraArgs); tok != "" {
			src, _ := c.Source("profile." + prof.Name + ".agent_extra_args")
			out = append(out, fmt.Sprintf("profile %q: agent_extra_args embed %s (%s); define an agent with a %s attribute instead",
				prof.Name, tok, c.displayLayer(src.Layer), attrForFlag(tok)))
		}
	}

	model := effectiveModel(prof, ac, cliModel)
	if model == "" {
		out = append(out, fmt.Sprintf("%s: no model pinned; the %s CLI will pick its own default (often the most expensive). Set model = \"...\" in %s",
			label, ac.Command, c.editHint(ac.Name)))
		return out
	}

	if c.ExpensiveOutputPerMTok != nil && *c.ExpensiveOutputPerMTok > 0 && ac.Pricing != nil {
		if mp, ok := c.modelPrice(ac, model); ok && mp.OutputPerMTok > *c.ExpensiveOutputPerMTok {
			by := "--model"
			if cliModel == "" {
				src, _ := c.modelPin(prof, ac)
				by = c.displayLayer(src.Layer)
			}
			out = append(out, fmt.Sprintf("%s: model %q costs $%.2f per million output tokens, above expensive_output_per_mtok = %g (pinned by %s)",
				label, model, mp.OutputPerMTok, *c.ExpensiveOutputPerMTok, by))
		}
	}
	return out
}

// effectiveModel is the model a run of ac uses: the CLI override, else the
// profile's pin, else the model attribute, else one hidden in args or the
// profile's agent_extra_args.
func effectiveModel(prof *ProfileConfig, ac *AgentConfig, cliModel string) string {
	model := cliModel
	if model == "" && prof != nil {
		model = prof.Model
	}
	if model == "" {
		model = ac.Model
	}
	if model == "" {
		model = argsModel(ac.Args)
	}
	if model == "" && prof != nil {
		model = argsModel(prof.AgentExtraArgs)
	}
	return model
}

// ModelNameError rejects a model id that is neither in the agent's pricing
// table (dated suffix ignored) nor matches its model_pattern. Agents
// without a pattern accept any id. The message lists the known ids and
// the provider's model page (model_docs) when the agent names one.
func (c *Config) ModelNameError(prof *ProfileConfig, ac *AgentConfig, cliModel string) error {
	if ac == nil || ac.ModelPattern == "" {
		return nil
	}
	model := effectiveModel(prof, ac, cliModel)
	if model == "" {
		return nil
	}
	if ac.Pricing != nil {
		if _, ok := c.modelPrice(ac, model); ok {
			return nil
		}
	}
	re, err := regexp.Compile(ac.ModelPattern)
	if err != nil {
		return err
	}
	if re.MatchString(model) {
		return nil
	}
	by := "--model"
	if cliModel == "" {
		src, _ := c.modelPin(prof, ac)
		by = c.Where(src, display.TildeHome)
	}
	lines := []string{fmt.Sprintf("agent %q: model %q does not look like a %s model id (set by %s)", ac.Name, model, ac.TypeName(), by)}
	if names := SortedModels(ac.Pricing); len(names) > 0 {
		lines = append(lines, "known models: "+strings.Join(names, ", "))
	}
	if ac.ModelDocs != "" {
		lines = append(lines, "reference: "+ac.ModelDocs)
	}
	lines = append(lines, "--skip-model-name-check runs it anyway")
	return errors.New(strings.Join(lines, "\n  "))
}

// modelPin returns where the pinned model of a run comes from, by
// precedence: the profile's model, the agent's context block, the model
// attribute, else the args that embed it.
func (c *Config) modelPin(prof *ProfileConfig, ac *AgentConfig) (Contribution, bool) {
	if prof != nil && prof.Model != "" {
		return c.Source("profile." + prof.Name + ".model")
	}
	if src, ok := c.AttrSource(ac, "model"); ok {
		return src, true
	}
	return c.Source("agent." + ac.Name + ".args")
}

// validateModelPatterns compiles every agent's model_pattern once so a
// bad regexp is reported at load with its source, not at run start.
func (c *Config) validateModelPatterns() error {
	for name, ac := range c.Agents {
		if ac.ModelPattern == "" {
			continue
		}
		if _, err := regexp.Compile(ac.ModelPattern); err != nil {
			src, _ := c.Source("agent." + name + ".model_pattern")
			return fmt.Errorf("agent %q: model_pattern (%s): %v", name, c.Where(src, display.TildeHome), err)
		}
	}
	return nil
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

// displayLayer shortens a layer label for messages: paths under $HOME
// become ~/..., the built-in label and base labels pass through.
func (c *Config) displayLayer(label string) string {
	if label == "" {
		return BuiltinLayer
	}
	return display.TildeHome(label)
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
