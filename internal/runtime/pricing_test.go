package runtime

import (
	"strings"
	"testing"
	"time"
)

func TestBuiltinPricingMetadata(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.PricingAge(time.Now()); !ok {
		t.Errorf("built-in pricing_as_of must parse, got %q", cfg.PricingAsOf)
	}
	p := cfg.Agents["claude"].Pricing
	if p == nil || p.SuggestedModel == "" {
		t.Fatal("claude pricing must name a suggested_model")
	}
	if _, ok := p.Models[p.SuggestedModel]; !ok {
		t.Errorf("suggested_model %q must be priced", p.SuggestedModel)
	}
	if cfg.Agents["codex"].Pricing.SuggestedModel == "" {
		t.Error("codex pricing must name a suggested_model")
	}
	if src, _ := cfg.Source("pricing_as_of"); src.Layer != BuiltinLayer {
		t.Errorf("pricing_as_of source: %+v", src)
	}
}

func TestPricingAge(t *testing.T) {
	cfg := &Config{PricingAsOf: "2026-01-01"}
	age, ok := cfg.PricingAge(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if !ok || age != 90*24*time.Hour {
		t.Errorf("age = %v ok=%v", age, ok)
	}
	if _, ok := (&Config{PricingAsOf: "soon"}).PricingAge(time.Now()); ok {
		t.Error("unparsable date must not be ok")
	}
	if _, ok := (&Config{}).PricingAge(time.Now()); ok {
		t.Error("missing date must not be ok")
	}
}

func TestRenderPricingSectionRoundTrip(t *testing.T) {
	builtin, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	section := RenderPricingSection(builtin, "test")
	parsed, err := ParseDefaults([]byte(section), "section")
	if err != nil {
		t.Fatalf("rendered section does not parse: %v\n%s", err, section)
	}
	if parsed.PricingAsOf != builtin.PricingAsOf {
		t.Errorf("pricing_as_of: %q vs %q", parsed.PricingAsOf, builtin.PricingAsOf)
	}
	for _, name := range []string{"claude", "codex"} {
		want, got := builtin.Agents[name].Pricing, parsed.Agents[name].Pricing
		if got == nil || got.SuggestedModel != want.SuggestedModel || got.DefaultModel != want.DefaultModel || len(got.Models) != len(want.Models) {
			t.Errorf("%s pricing differs after round trip: %+v vs %+v", name, got, want)
			continue
		}
		for m, mp := range want.Models {
			if got.Models[m] != mp {
				t.Errorf("%s model %s: %+v vs %+v", name, m, got.Models[m], mp)
			}
		}
	}
	// The mock agent has pricing too and must round-trip without a suggested model.
	if parsed.Agents["mock"].Pricing == nil {
		t.Error("mock pricing missing")
	}

	// Replacing: appended once, then replaced in place.
	content := ReplacePricingSection("agent \"claude\" {\n  model = \"opus\"\n}\n", section)
	again := ReplacePricingSection(content, strings.Replace(section, "test", "second", 1))
	if strings.Count(again, PricingSectionStart) != 1 || !strings.Contains(again, "fetched from second") || !strings.HasPrefix(again, "agent \"claude\"") {
		t.Errorf("replace in place failed:\n%s", again)
	}
	if _, err := ParseDefaults([]byte(again), "again"); err != nil {
		t.Errorf("replaced content does not parse: %v", err)
	}
}

func TestSortedModelsCheapestFirst(t *testing.T) {
	cfg, _ := Load(nil)
	names := SortedModels(cfg.Agents["claude"].Pricing)
	p := cfg.Agents["claude"].Pricing.Models
	for i := 1; i < len(names); i++ {
		if p[names[i-1]].OutputPerMTok > p[names[i]].OutputPerMTok {
			t.Errorf("not sorted: %s before %s", names[i-1], names[i])
		}
	}
	if SortedModels(nil) != nil {
		t.Error("nil pricing")
	}
}
