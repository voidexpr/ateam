package prompts

import (
	"errors"

	"github.com/ateam/internal/prompts/assembler"
)

// Vars is the variable resolver consumed by the assembler engine and surfaced
// to dynamics via ResolveContext.Vars(). Aliased so prompt-authoring code
// doesn't have to reach into the assembler subpackage.
type Vars = assembler.Vars

// Section is the per-section provenance record returned by Prompt.Inspect.
// Aliased from the assembler so the composition machinery and the Inspect
// surface agree on a single struct.
type Section = assembler.Section

// ResolveMode distinguishes a verification/preview render from a real
// execution render. Dynamics inspect Mode to decide whether to return real
// data or a stable sentinel that keeps preview output deterministic.
type ResolveMode int

const (
	// ModeReal renders for actual execution: dynamics return real data and
	// errors surface to the caller.
	ModeReal ResolveMode = iota
	// ModePreview renders for verification, --paths, --plan-only and other
	// inspection paths. Dynamics that depend on runtime-only artifacts
	// return a sentinel instead of fabricating output.
	ModePreview
)

// ResolveContext is the surface Prompt.Resolve and every dynamic function
// receive. flow.Runtime satisfies it; tests construct ad-hoc impls. Keeping
// the interface in the prompts package keeps prompt authors out of the flow
// import (no cycle).
//
// The spec also calls for `Env() *root.ResolvedEnv`. That method is
// intentionally deferred — root already imports prompts, so adding the
// reverse dependency requires extracting the helpers in prompts.go (roles,
// project-info, defaults installer) into a lower package first. No
// shipped dynamic needs Env access today; the method will be added once
// the cycle is broken.
type ResolveContext interface {
	// Vars returns the variable resolver dynamics consult for namespaced
	// values. Same shape as the engine consumes during {{ns.key}}.
	Vars() Vars
	// Mode returns the current resolve mode.
	Mode() ResolveMode
	// Dynamics returns the registered dynamics available at this render.
	// Surfaced so dynamics can compose by invoking each other through the
	// same engine machinery without smuggling a dispatcher reference.
	Dynamics() PromptDynamic
}

// Prompt is what a verb hands the flow framework. Each impl owns its own
// state (file path, inline text, framing options, …) and produces final
// text in Resolve. Inspect returns the section break-down used by
// --paths / --inline-paths; literal-text impls return (nil, nil).
type Prompt interface {
	Resolve(ctx ResolveContext) (string, error)
	Inspect(ctx ResolveContext) ([]Section, error)
}

// RawTextPrompt holds literal text that is NOT expanded — used by
// `ateam exec --raw` (and similar) where authors want byte-for-byte fidelity
// with the source string. No variable substitution, no includes, no
// dynamics.
type RawTextPrompt struct {
	Text string
}

func (p RawTextPrompt) Resolve(ResolveContext) (string, error) { return p.Text, nil }
func (p RawTextPrompt) Inspect(ResolveContext) ([]Section, error) {
	return nil, nil
}

// PromptText holds literal text WITH expansion. Variable substitution and
// dynamics apply. Include directives error because PromptText has no
// anchor list — authors who need includes use PromptFile instead.
type PromptText struct {
	Text string
}

func (p PromptText) Resolve(ctx ResolveContext) (string, error) {
	return renderWithCtx(nil, p.Text, ctx)
}

func (p PromptText) Inspect(ResolveContext) ([]Section, error) {
	return nil, nil
}

// PromptFile points at an anchored .prompt.md (or a logical role name) and
// composes the standard framing (root pre, dir pre, role main, role post,
// dir post) before expanding the assembled body.
//
// Path is interpreted in one of two ways depending on its shape:
//
//  1. Logical name — no path separator, no ".prompt.md" suffix.
//     Examples: "review", "code", "report/security". Resolved via the
//     standard anchor walk (project → org → embedded).
//
//  2. Filesystem path — ends in ".prompt.md", contains either a "/" or
//     starts with ".". Resolved by injecting the file's parent dir as a
//     temporary anchor at the front of the chain so sibling
//     <basename>.pre.*.md and dir-level _pre.*.md in that dir compose.
//     Step 9 wires the temporary-anchor injection.
//
// Assembler and Vars are factory-injected. The spec's eventual shape lifts
// both to ctx (so the bundle's Prompt is just `{Path, Pre, Post,
// CustomBody}` per spec lines 195-222). Step 4 keeps them on PromptFile so
// the migration can land without first restructuring flow.Runtime's vars
// builder; a follow-up step moves them to ResolveContext.
type PromptFile struct {
	Path       string
	PrePrompt  string
	PostPrompt string
	CustomBody string

	Assembler *assembler.Assembler
	Vars      Vars
}

// Resolve composes the standard framing for p.Path via Assembler and
// returns the rendered body. PrePrompt / PostPrompt / CustomBody feed the
// AssembleOptions surface.
func (p PromptFile) Resolve(ctx ResolveContext) (string, error) {
	res, err := p.assemble(ctx)
	if err != nil {
		return "", err
	}
	return res.Prompt, nil
}

// Inspect returns the section breakdown so --paths / --inline-paths can
// display each composed fragment's anchor + path provenance.
func (p PromptFile) Inspect(ctx ResolveContext) ([]Section, error) {
	res, err := p.assemble(ctx)
	if err != nil {
		return nil, err
	}
	return res.Sections, nil
}

func (p PromptFile) assemble(ctx ResolveContext) (assembler.AssembleResult, error) {
	if p.Assembler == nil {
		return assembler.AssembleResult{}, errors.New("prompts.PromptFile: missing Assembler (factory must inject env.Assembler())")
	}
	if p.Path == "" {
		return assembler.AssembleResult{}, errors.New("prompts.PromptFile: empty Path")
	}
	vars := p.Vars
	if vars == nil && ctx != nil {
		vars = ctx.Vars()
	}
	engine := assembler.NewEngine(p.Assembler, 0)
	if ctx != nil {
		if dyn := ctx.Dynamics(); dyn != nil {
			engine = engine.WithDispatcher(NewDispatcher(dyn, ctx))
		}
	}
	opts := &assembler.AssembleOptions{
		ReplaceRoleMain: p.CustomBody,
		PrePrompt:       p.PrePrompt,
		PostPrompt:      p.PostPrompt,
	}
	return p.Assembler.Assemble(p.Path, vars, engine, opts)
}

// renderWithCtx runs the assembler engine against ctx — vars + dynamics
// wired up — using the supplied anchor list (nil for inline-text impls
// that don't support includes).
func renderWithCtx(a *assembler.Assembler, text string, ctx ResolveContext) (string, error) {
	e := assembler.NewEngine(a, 0)
	if ctx == nil {
		return e.Render(text, nil)
	}
	if dyn := ctx.Dynamics(); dyn != nil {
		e = e.WithDispatcher(NewDispatcher(dyn, ctx))
	}
	return e.Render(text, ctx.Vars())
}
