package agent

import (
	"regexp"
)

// ModelPrice holds per-token pricing for a model.
type ModelPrice struct {
	InputPerToken  float64
	OutputPerToken float64
}

// PricingTable maps normalized model names to their per-token prices.
type PricingTable map[string]ModelPrice

// Clone returns a deep copy so per-task agent clones don't share map
// backing memory with the shared original. Pricing is read-only in
// practice, but cloning removes the invariant-by-convention: concurrent
// pool workers operate on independent maps.
func (t PricingTable) Clone() PricingTable {
	if t == nil {
		return nil
	}
	cp := make(PricingTable, len(t))
	for k, v := range t {
		cp[k] = v
	}
	return cp
}

var dateSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// NormalizeModel strips a trailing -YYYY-MM-DD date suffix from a model name.
func NormalizeModel(model string) string {
	return dateSuffix.ReplaceAllString(model, "")
}

// EstimateCost returns an estimated cost in USD for the given model and token counts.
// It first tries the reported model, then falls back to defaultModel.
// Returns 0 if neither is found or if the table is nil.
func EstimateCost(table PricingTable, model, defaultModel string, inputTokens, outputTokens int) float64 {
	if table == nil {
		return 0
	}
	p, ok := table[NormalizeModel(model)]
	if !ok {
		p, ok = table[NormalizeModel(defaultModel)]
		if !ok {
			return 0
		}
	}
	return float64(inputTokens)*p.InputPerToken + float64(outputTokens)*p.OutputPerToken
}
