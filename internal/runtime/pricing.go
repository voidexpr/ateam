package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PricingAge returns how old the pricing tables are at now, parsed from
// PricingAsOf (YYYY-MM-DD). ok is false when no date is set or it does
// not parse.
func (c *Config) PricingAge(now time.Time) (age time.Duration, ok bool) {
	if c.PricingAsOf == "" {
		return 0, false
	}
	t, err := time.Parse("2006-01-02", c.PricingAsOf)
	if err != nil {
		return 0, false
	}
	return now.Sub(t), true
}

// PricingMaxAge is how old the pricing tables may be before `ateam env`
// suggests refreshing them.
const PricingMaxAge = 90 * 24 * time.Hour

// ParseDefaults parses a standalone ateam.hcl (e.g. a freshly fetched copy
// of the embedded defaults) without the built-in layer underneath.
func ParseDefaults(data []byte, label string) (*Config, error) {
	cfg := newConfig()
	if err := mergeHCL(cfg, data, label, label); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Markers delimiting the section `ateam setup --refresh-models` owns in a
// layer's ateam.hcl, so a later refresh replaces it in place.
const (
	PricingSectionStart = "// >>> ateam pricing (managed by ateam setup --refresh-models; edits inside are overwritten)"
	PricingSectionEnd   = "// <<< ateam pricing"
)

// RenderPricingSection renders the pricing_as_of scalar and the pricing
// block of every agent in cfg that has one, as HCL between the section
// markers. A pricing block replaces the whole inherited block when merged,
// so each one is written in full.
func RenderPricingSection(cfg *Config, origin string) string {
	var b strings.Builder
	b.WriteString(PricingSectionStart + "\n")
	fmt.Fprintf(&b, "// fetched from %s\n", origin)
	if cfg.PricingAsOf != "" {
		fmt.Fprintf(&b, "pricing_as_of = %q\n", cfg.PricingAsOf)
	}
	names := make([]string, 0, len(cfg.Agents))
	for name, ac := range cfg.Agents {
		if ac.Pricing != nil && len(ac.Pricing.Models) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		p := cfg.Agents[name].Pricing
		fmt.Fprintf(&b, "\nagent %q {\n  pricing {\n", name)
		if p.DefaultModel != "" {
			fmt.Fprintf(&b, "    default_model   = %q\n", p.DefaultModel)
		}
		if p.SuggestedModel != "" {
			fmt.Fprintf(&b, "    suggested_model = %q\n", p.SuggestedModel)
		}
		models := make([]string, 0, len(p.Models))
		for m := range p.Models {
			models = append(models, m)
		}
		sort.Strings(models)
		for _, m := range models {
			mp := p.Models[m]
			fmt.Fprintf(&b, "    model %q {\n      input_per_mtok        = %s\n      cached_input_per_mtok = %s\n      output_per_mtok       = %s\n    }\n",
				m, ftoa(mp.InputPerMTok), ftoa(mp.CachedInputPerMTok), ftoa(mp.OutputPerMTok))
		}
		b.WriteString("  }\n}\n")
	}
	b.WriteString(PricingSectionEnd + "\n")
	return b.String()
}

func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ReplacePricingSection returns content with its managed pricing section
// replaced by section, or appended when there is none.
func ReplacePricingSection(content, section string) string {
	start := strings.Index(content, PricingSectionStart)
	end := strings.Index(content, PricingSectionEnd)
	if start >= 0 && end > start {
		end += len(PricingSectionEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		return content[:start] + section + content[end:]
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if content != "" {
		content += "\n"
	}
	return content + section
}

// SortedModels lists an agent's priced models cheapest output first.
func SortedModels(p *AgentPricing) []string {
	if p == nil {
		return nil
	}
	names := make([]string, 0, len(p.Models))
	for m := range p.Models {
		names = append(names, m)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := p.Models[names[i]], p.Models[names[j]]
		if a.OutputPerMTok != b.OutputPerMTok {
			return a.OutputPerMTok < b.OutputPerMTok
		}
		return names[i] < names[j]
	})
	return names
}
