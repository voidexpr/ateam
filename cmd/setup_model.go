package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
)

// DefaultsURL is where `ateam setup --refresh-models` fetches the current
// embedded defaults (pricing tables, suggested models) from. Override with
// ATEAM_DEFAULTS_URL (tests, mirrors).
const DefaultsURL = "https://raw.githubusercontent.com/voidexpr/ateam/main/defaults/ateam.hcl"

const (
	modelChoiceSuggested = "suggested"
	modelChoiceNone      = "none"
)

// setupModelPins proposes a model pin per agent type: the pricing table's
// suggested_model (the tier below the top, current generation) with its
// price, the alternatives, and what runs today. choice is "suggested",
// "none", a model id, or "" to ask; layer is where the pin is written.
func setupModelPins(env *root.ResolvedEnv, choice, layer string, agents []string) error {
	if layer != agentConfigHome && layer != agentConfigProject {
		return fmt.Errorf("--layer must be home or project (got %q)", layer)
	}
	if len(agents) == 0 {
		agents = agentCLIsOnPath()
		if len(agents) == 0 {
			fmt.Println("No agent CLI (claude, codex) found on PATH; nothing to set up.")
			return nil
		}
	}
	rt := env.Runtime
	interactive := choice == "" && !stdinIsPiped() && isTerminal()
	for _, agentType := range agents {
		ac, ok := rt.Agents[agentType]
		if !ok {
			return fmt.Errorf("unknown agent %q (expected claude or codex)", agentType)
		}
		rep := rt.ModelReport(nil, &ac, runtime.CLIOverrides{}, nativeConfigFor(env, rt, nil, &ac))
		pinned := rep.Model.Value != "" && rep.Model.Source != runtime.SourceAgentDefault && !strings.HasPrefix(rep.Model.Source, "~/") && !strings.Contains(rep.Model.Note, "agent-native")
		fmt.Printf("%s: model %s\n", agentType, rep.ModelText())
		if ac.Pricing == nil || ac.Pricing.SuggestedModel == "" {
			fmt.Printf("  no suggestion available (no suggested_model in the %s pricing table)\n", agentType)
			continue
		}
		suggested := ac.Pricing.SuggestedModel
		if pinned && choice == "" {
			fmt.Printf("  already pinned; pass --model-choice %s (or a model id) to change it\n", modelChoiceSuggested)
			continue
		}
		printModelMenu(ac.Pricing, suggested, rep.Model.Value)

		pick := choice
		if pick == "" {
			if !interactive {
				fmt.Printf("  not pinned; run 'ateam setup --model' in a terminal or pass --model-choice %s|none|<model id>\n", modelChoiceSuggested)
				continue
			}
			pick = promptModelChoice(suggested)
		}
		switch pick {
		case modelChoiceNone:
			fmt.Printf("  %s: left as is\n", agentType)
			continue
		case modelChoiceSuggested:
			pick = suggested
		}
		if _, known := ac.Pricing.Models[pick]; !known {
			fmt.Printf("  note: %q is not in the pricing table; cost estimates and the expensive-model warning will not know it\n", pick)
		}
		file, err := layerFile(env, layer)
		if err != nil {
			return err
		}
		if err := applyModelPin(file, agentType, pick); err != nil {
			return err
		}
		fmt.Printf("  %s: model = %q written to %s\n", agentType, pick, display.TildeHome(file))
	}
	return nil
}

func layerFile(env *root.ResolvedEnv, layer string) (string, error) {
	switch layer {
	case agentConfigHome:
		return filepath.Join(env.HomeDir, root.ConfigFile), nil
	case agentConfigProject:
		if env.ProjectDir == "" {
			return "", fmt.Errorf("--layer project needs a project; run from one or use --layer home")
		}
		return filepath.Join(env.ProjectDir, root.ConfigFile), nil
	}
	return "", fmt.Errorf("unknown layer %q", layer)
}

func printModelMenu(p *runtime.AgentPricing, suggested, current string) {
	for _, m := range runtime.SortedModels(p) {
		mp := p.Models[m]
		mark := "  "
		switch {
		case m == suggested && m == current:
			mark = "* ="
		case m == suggested:
			mark = "* "
		case m == current:
			mark = "= "
		}
		fmt.Printf("  %s %-24s $%6.2f in / $%6.2f out per MTok\n", mark, m, mp.InputPerMTok, mp.OutputPerMTok)
	}
	fmt.Printf("  (* suggested: the tier below the top model; = what runs today)\n")
}

func promptModelChoice(suggested string) string {
	fmt.Fprintf(os.Stderr, "Pin a model for unattended runs?\n  1) %s  — the suggestion [default]\n  2) none  — leave the CLI's own default\n  or type a model id\n> ", suggested)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return modelChoiceNone
	}
	switch answer := strings.TrimSpace(scanner.Text()); answer {
	case "", "1":
		return modelChoiceSuggested
	case "2", modelChoiceNone:
		return modelChoiceNone
	default:
		return answer
	}
}

// applyModelPin appends an agent block setting model to file; a second
// block with the same name merges per attribute.
func applyModelPin(file, agentType, model string) error {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", file, err)
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n// Added by ateam setup: pin the model for unattended %s runs so a new\n// CLI default cannot silently switch them to a pricier model.\nagent %q {\n  model = %q\n}\n", agentType, agentType, model)
	return err
}

// refreshPricing fetches the current embedded defaults from DefaultsURL
// (or ATEAM_DEFAULTS_URL) and writes their pricing tables, suggested
// models and pricing_as_of into the managed section of ~/.ateam/ateam.hcl.
func refreshPricing(env *root.ResolvedEnv) error {
	url := os.Getenv("ATEAM_DEFAULTS_URL")
	if url == "" {
		url = DefaultsURL
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("cannot fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cannot fetch %s: HTTP %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", url, err)
	}
	fetched, err := runtime.ParseDefaults(data, url)
	if err != nil {
		return fmt.Errorf("fetched defaults do not parse: %w", err)
	}
	if fetched.PricingAsOf == "" {
		return fmt.Errorf("fetched defaults carry no pricing_as_of; refusing to apply")
	}
	if err := root.EnsureHome(env.HomeDir); err != nil {
		return err
	}
	file := filepath.Join(env.HomeDir, root.ConfigFile)
	current, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	section := runtime.RenderPricingSection(fetched, url)
	if err := os.WriteFile(file, []byte(runtime.ReplacePricingSection(string(current), section)), 0644); err != nil {
		return err
	}
	fmt.Printf("pricing as of %s written to %s\n", fetched.PricingAsOf, display.TildeHome(file))
	for name, ac := range fetched.Agents {
		if ac.Pricing != nil && len(ac.Pricing.Models) > 0 {
			fmt.Printf("  %s: %d models, suggested %s\n", name, len(ac.Pricing.Models), ac.Pricing.SuggestedModel)
		}
	}
	return nil
}

// pricingStaleWarning returns the warning `ateam env` prints when the
// pricing tables are older than runtime.PricingMaxAge, else "".
func pricingStaleWarning(rtCfg *runtime.Config, now time.Time) string {
	age, ok := rtCfg.PricingAge(now)
	if !ok || age <= runtime.PricingMaxAge {
		return ""
	}
	return fmt.Sprintf("pricing tables are from %s (%d days old); run 'ateam setup --refresh-models' to refresh prices and suggested models",
		rtCfg.PricingAsOf, int(age.Hours()/24))
}
