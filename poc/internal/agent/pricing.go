package agent

import (
	"regexp"
)

// ModelPrice holds per-token pricing for a model.
type ModelPrice struct {
	InputPerToken  float64
	OutputPerToken float64
}

var pricing = map[string]ModelPrice{
	"gpt-4o":           {2.50 / 1e6, 10.00 / 1e6},
	"gpt-4o-mini":      {0.15 / 1e6, 0.60 / 1e6},
	"gpt-4.1":          {2.00 / 1e6, 8.00 / 1e6},
	"gpt-4.1-mini":     {0.40 / 1e6, 1.60 / 1e6},
	"gpt-4.1-nano":     {0.10 / 1e6, 0.40 / 1e6},
	"o3":               {2.00 / 1e6, 8.00 / 1e6},
	"o3-mini":          {1.10 / 1e6, 4.40 / 1e6},
	"o4-mini":          {1.10 / 1e6, 4.40 / 1e6},
	"codex-mini":       {1.50 / 1e6, 6.00 / 1e6},
	"claude-opus-4":    {15.00 / 1e6, 75.00 / 1e6},
	"claude-sonnet-4":  {3.00 / 1e6, 15.00 / 1e6},
	"claude-haiku-4":   {0.80 / 1e6, 4.00 / 1e6},
}

var dateSuffix = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)

// NormalizeModel strips a trailing -YYYY-MM-DD date suffix from a model name.
func NormalizeModel(model string) string {
	return dateSuffix.ReplaceAllString(model, "")
}

// EstimateCost returns an estimated cost in USD for the given model and token counts.
// Returns 0 for unknown models.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	p, ok := pricing[NormalizeModel(model)]
	if !ok {
		return 0
	}
	return float64(inputTokens)*p.InputPerToken + float64(outputTokens)*p.OutputPerToken
}
