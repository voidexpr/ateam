package agent

import (
	"regexp"
)

// ModelPrice holds per-token pricing for a model.
//
// CachedInputPerToken is the rate billed for cached input tokens (e.g.
// OpenAI's prompt cache, typically 1/10 of InputPerToken). When zero,
// EstimateCost charges cached tokens at InputPerToken — preserving the
// pre-cache-aware behavior for models without a published cached rate.
type ModelPrice struct {
	InputPerToken       float64
	CachedInputPerToken float64
	OutputPerToken      float64
}

// PricingTable maps normalized model names to their per-token prices.
type PricingTable map[string]ModelPrice

// Clone returns a deep copy so per-agent exec clones don't share map
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

// modelContextWindows holds the input-token context window for known
// models. Values match what claude reports in the result event's
// modelUsage payload.
var modelContextWindows = map[string]int{
	"claude-sonnet-4-6": 200000,
	"claude-haiku-4-5":  200000,
	"claude-opus-4-7":   200000,
	"claude-sonnet-4-5": 200000,
	"claude-opus-4-6":   200000,
	"claude-haiku-4":    200000,

	// OpenAI / codex models
	"gpt-5":         400000,
	"gpt-5-mini":    400000,
	"gpt-5-nano":    400000,
	"gpt-5-codex":   400000,
	"gpt-5.1-codex": 400000,
	"gpt-5.2-codex": 400000,
	"gpt-5.3-codex": 400000,
	"gpt-5.4-codex": 400000,
	"gpt-4o":        128000,
	"gpt-4o-mini":   128000,
	// 1,047,576 = OpenAI's published gpt-4.1 / gpt-4.1-mini context window
	// (not a round 1M). Matters for ctx% accuracy in tail/ps.
	"gpt-4.1":      1047576,
	"gpt-4.1-mini": 1047576,
	"o3":           200000,
	"o4-mini":      200000,
}

// ContextWindow returns the input-token context window for the given
// model name (after NormalizeModel). Returns 0 when the model isn't
// known — callers should treat that as "unknown" and skip the percentage
// display rather than dividing by zero.
func ContextWindow(model string) int {
	if model == "" {
		return 0
	}
	return modelContextWindows[NormalizeModel(model)]
}

// EstimateCost returns an estimated cost in USD for the given model and token counts.
// It first tries the reported model, then falls back to defaultModel.
// Returns 0 if neither is found or if the table is nil.
//
// inputTokens is the *total* prompt tokens reported by the agent (including
// any portion served from cache). cachedInputTokens is the cached subset.
// Cached tokens are billed at CachedInputPerToken when set, else at the
// regular InputPerToken — so callers don't have to special-case models
// without a published cached rate.
func EstimateCost(table PricingTable, model, defaultModel string, inputTokens, cachedInputTokens, outputTokens int) float64 {
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
	cachedRate := p.CachedInputPerToken
	if cachedRate == 0 {
		cachedRate = p.InputPerToken
	}
	if cachedInputTokens > inputTokens {
		cachedInputTokens = inputTokens
	}
	uncached := inputTokens - cachedInputTokens
	return float64(uncached)*p.InputPerToken +
		float64(cachedInputTokens)*cachedRate +
		float64(outputTokens)*p.OutputPerToken
}
