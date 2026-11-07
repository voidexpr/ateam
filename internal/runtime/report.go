package runtime

import (
	"fmt"
	"strings"
)

// Attribution is a resolved value and what set it.
type Attribution struct {
	Value  string
	Source string // layer label, LayerCLI, SourceAgentDefault, or "" when unset
	Note   string // e.g. "inside args, outranked by the model attribute"
}

// ChainEntry is one contribution to the model/effort decision. Empty
// Model/Effort mean the entry is silent on that slot.
type ChainEntry struct {
	Layer  string // layer label, `agent "x" (base)`, or LayerCLI
	Model  string
	Effort string
	Note   string
}

// CLIOverrides are the per-run flag values that outrank the attributes
// (--model / --cheaper-model, --effort).
type CLIOverrides struct {
	Model  string
	Effort string
}

// ModelReport explains which model and effort an agent will run with and
// why. Chain lists every contribution in precedence order, lowest first,
// silent layers included; Model and Effort are the fold of the chain
// (last non-empty wins per slot). The precedence lives only in the order
// of Chain:
//
//  1. the agent CLI's own configuration (settings.json, config.toml, env),
//     used only when nothing ateam passes sets the slot
//  2. --model / --effort tokens inside the agent's args (the model and
//     effort attributes are appended after them on the command line)
//  3. the model / effort attributes, per layer in load order, with a
//     value inherited through `base` shown as the base agent
//  4. CLI overrides
//  5. tokens inside the profile's agent_extra_args, appended last
type ModelReport struct {
	Agent     string
	AgentType string
	Chain     []ChainEntry
	Model     Attribution
	Effort    Attribution
}

const (
	LayerCLI           = "cli"
	SourceAgentDefault = "agent default"
	noteArgs           = "inside args, outranked by the model/effort attributes"
	noteExtraArgs      = "inside agent_extra_args, appended last on the command line"
)

// ModelReport builds the report for an agent and the profile that selected
// it (prof may be nil). native is the agent CLI's own configuration, the
// lowest entry of the chain (an empty Source omits it). Pure computation
// over the loaded config.
func (c *Config) ModelReport(prof *ProfileConfig, ac *AgentConfig, cli CLIOverrides, native NativeConfig) ModelReport {
	r := ModelReport{Agent: ac.Name, AgentType: ac.TypeName()}
	key := "agent." + ac.Name + "."

	if native.Source != "" {
		note := "agent-native default, used when nothing above sets the slot"
		if native.Note != "" {
			note = native.Note
		}
		r.Chain = append(r.Chain, ChainEntry{Layer: native.Source, Model: native.Model, Effort: native.Effort, Note: note})
	}

	if m, e := argsModel(ac.Args), argsEffort(ac.Args); m != "" || e != "" {
		src, _ := c.Source(key + "args")
		r.Chain = append(r.Chain, ChainEntry{Layer: c.displayLayer(src.Layer), Model: m, Effort: e, Note: noteArgs})
	}

	modelChain := c.Sources[key+"model"]
	effortChain := c.Sources[key+"effort"]
	for _, label := range c.attributeLayers(modelChain, effortChain) {
		entry := ChainEntry{Layer: c.displayLayer(label.name)}
		if !label.found {
			entry.Note = "no file"
		}
		for _, contrib := range modelChain {
			if contrib.Layer == label.name {
				entry.Model = contrib.Value
			}
		}
		for _, contrib := range effortChain {
			if contrib.Layer == label.name {
				entry.Effort = contrib.Value
			}
		}
		r.Chain = append(r.Chain, entry)
	}

	if cli.Model != "" || cli.Effort != "" {
		r.Chain = append(r.Chain, ChainEntry{Layer: LayerCLI, Model: cli.Model, Effort: cli.Effort})
	}

	if prof != nil {
		if m, e := argsModel(prof.AgentExtraArgs), argsEffort(prof.AgentExtraArgs); m != "" || e != "" {
			src, _ := c.Source("profile." + prof.Name + ".agent_extra_args")
			r.Chain = append(r.Chain, ChainEntry{Layer: c.displayLayer(src.Layer), Model: m, Effort: e, Note: noteExtraArgs})
		}
	}

	for _, entry := range r.Chain {
		if entry.Model != "" {
			r.Model = Attribution{Value: entry.Model, Source: entry.Layer, Note: entry.Note}
		}
		if entry.Effort != "" {
			r.Effort = Attribution{Value: entry.Effort, Source: entry.Layer, Note: entry.Note}
		}
	}
	if r.Model.Value == "" {
		r.Model.Source = SourceAgentDefault
	}
	return r
}

type layerRef struct {
	name  string
	found bool
}

// attributeLayers lists the layers to show for the attribute slots: any
// base-agent pseudo-layer present in the chains first (lowest precedence,
// since a child's own value always wins), then every loaded layer in
// order, found or not.
func (c *Config) attributeLayers(chains ...[]Contribution) []layerRef {
	known := make(map[string]bool, len(c.Layers))
	for _, l := range c.Layers {
		known[l.Label] = true
	}
	var out []layerRef
	seen := make(map[string]bool)
	for _, chain := range chains {
		for _, contrib := range chain {
			if !known[contrib.Layer] && !seen[contrib.Layer] {
				seen[contrib.Layer] = true
				out = append(out, layerRef{name: contrib.Layer, found: true})
			}
		}
	}
	for _, l := range c.Layers {
		out = append(out, layerRef{name: l.Label, found: l.Found})
	}
	return out
}

// Describe renders an attribution as "value (source, note)" or the unset
// text in parentheses.
func (a Attribution) Describe(unset string) string {
	if a.Value == "" {
		return "(" + unset + ")"
	}
	s := a.Value + " (" + a.Source
	if a.Note != "" {
		s += ", " + a.Note
	}
	return s + ")"
}

// ModelText and EffortText are the one-line answers.
func (r ModelReport) ModelText() string {
	return r.Model.Describe("not pinned: the " + r.AgentType + " CLI picks its own default")
}

func (r ModelReport) EffortText() string {
	return r.Effort.Describe("not set: the " + r.AgentType + " CLI default")
}

// ChainLines renders the chain one layer per line, lowest precedence first.
func (r ModelReport) ChainLines() []string {
	lines := make([]string, 0, len(r.Chain))
	for _, e := range r.Chain {
		var parts []string
		if e.Model != "" {
			parts = append(parts, "model "+e.Model)
		}
		if e.Effort != "" {
			parts = append(parts, "effort "+e.Effort)
		}
		text := "(nothing)"
		if len(parts) > 0 {
			text = strings.Join(parts, ", ")
		}
		if e.Note != "" {
			text += " — " + e.Note
		}
		lines = append(lines, fmt.Sprintf("%s: %s", e.Layer, text))
	}
	return lines
}
