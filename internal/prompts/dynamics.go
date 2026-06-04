package prompts

import (
	"fmt"

	"github.com/ateam/internal/prompts/assembler"
)

// PromptDynamicFunction is the contract for a single dynamic. The engine
// expands variables in the directive's arg list, tokenizes with shell-like
// quoting, then invokes the function with the resulting args.
type PromptDynamicFunction func(ctx ResolveContext, args ...string) (string, error)

// PromptDynamic is the per-invocation map of dynamic name → function. The
// CLI layer builds one at executor creation; tests build whatever they need.
// There is no global registry by design — every dynamic available at render
// time is on this map.
type PromptDynamic map[string]PromptDynamicFunction

// NewDispatcher adapts a PromptDynamic + ResolveContext into the engine's
// Dispatcher contract. The engine calls Dispatch with the already-tokenized
// args; this adapter looks up the function and forwards them with ctx.
func NewDispatcher(funcs PromptDynamic, ctx ResolveContext) assembler.Dispatcher {
	return &dispatcher{funcs: funcs, ctx: ctx}
}

type dispatcher struct {
	funcs PromptDynamic
	ctx   ResolveContext
}

func (d *dispatcher) Dispatch(name string, args []string) (string, error) {
	fn, ok := d.funcs[name]
	if !ok {
		return "", fmt.Errorf("{{dynamic.%s}}: unknown dynamic", name)
	}
	return fn(d.ctx, args...)
}
