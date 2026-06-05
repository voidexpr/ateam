package prompts

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
)

// filesystemPromptPath reports whether `path` looks like a filesystem
// reference (ends in .prompt.md AND contains a separator or starts with
// "."). PromptFile auto-wraps such paths in a TempAnchorAssembler for
// backward compatibility with direct callers; cmd-layer dispatch
// detects the same shape inline and injects the wrapper explicitly.
func filesystemPromptPath(path string) bool {
	if !strings.HasSuffix(path, ".prompt.md") {
		return false
	}
	return strings.ContainsRune(path, '/') || strings.HasPrefix(path, ".")
}

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

// ResolveContext is the surface Prompt.Resolve and every dynamic
// function receive. flow.Runtime satisfies it; tests construct ad-hoc
// impls. Keeping the interface in the prompts package keeps prompt
// authors out of the flow import (no cycle).
//
// Spec step 9: Env() is now part of the contract. The root→prompts
// cycle that previously blocked it was broken by extracting the
// data/helper code (AllRoleIDs, ProjectInfoParams, FormatProjectInfo,
// WriteOrgDefaults, AutoRolesMarker) into internal/promptdata. Both
// internal/root and internal/prompts now import promptdata; prompts
// safely imports root for ResolvedEnv.
type ResolveContext interface {
	// Env returns the resolved env at the time the context was built.
	// Dynamics that need project paths / config call ctx.Env() instead
	// of closing over env at factory construction time.
	Env() *root.ResolvedEnv
	// Vars returns the variable resolver dynamics consult for namespaced
	// values. Same shape as the engine consumes during {{ns.key}}.
	Vars() Vars
	// Mode returns the current resolve mode.
	Mode() ResolveMode
	// Dynamics returns the registered dynamics available at this render.
	// Surfaced so dynamics can compose by invoking each other through
	// the same engine machinery without smuggling a dispatcher
	// reference.
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
// Path is interpreted by the Assembler. The orchestrator delegates lookup
// to either p.Assembler (when supplied) or ctx.Env().Assembler() (the
// default project → org → embedded chain). Operators wanting to compose
// against an out-of-tree directory inject an assembler.TempAnchorAssembler
// at construction time; future Prompt impls (Python, Jinja) plug into the
// per-file rendering via p.Factory.
//
// PrePrompt and PostPrompt are RAW text — appended verbatim to the
// composed body. They do not flow through the engine. This is a
// deliberate consistency rule: the wrapper text reads identically
// regardless of the role main's file shape (`.prompt.md`, future
// `.prompt.py`, etc.). Authors who want template expansion in a wrapper
// place the value in a fragment file instead.
//
// CustomBody, when set, replaces the role_main file content — the
// factory and the role_main file read are skipped, and the body is
// engine-rendered as if it were a role_main `.prompt.md`. Framing
// fragments still compose around it.
type PromptFile struct {
	Path       string
	PrePrompt  string
	PostPrompt string
	CustomBody string

	// Assembler picks the lookup strategy. nil falls through to
	// ctx.Env().Assembler() — the standard chain. Set for special
	// dispatch (TempAnchorAssembler for filesystem-path .prompt.md
	// files; BasicAssembler for single-file rendering with no framing).
	Assembler assembler.Assembler

	// Factory selects the Prompt impl that renders the role_main file.
	// nil falls through to an internal default that renders the body
	// through the orchestrator's own engine (so {{include}} inside the
	// role main resolves against the same anchor chain). Explicit
	// factories ship in future for `.prompt.py` and similar.
	Factory PromptFactory
}

// Resolve composes the standard framing for p.Path via Assembler and
// returns the rendered body. PrePrompt / PostPrompt wrap the result as
// raw text; CustomBody replaces the role_main file body.
func (p PromptFile) Resolve(ctx ResolveContext) (string, error) {
	sections, err := p.composeSections(ctx)
	if err != nil {
		return "", err
	}
	body := joinSections(sections)
	return wrapRaw(p.PrePrompt, body, p.PostPrompt), nil
}

// Inspect returns the section breakdown so --paths / --inline-paths can
// display each composed fragment's anchor + path provenance. CLI
// wrappers (PrePrompt / PostPrompt) appear as their own sections so
// inspection callers see exactly what the agent receives.
func (p PromptFile) Inspect(ctx ResolveContext) ([]Section, error) {
	sections, err := p.composeSections(ctx)
	if err != nil {
		return nil, err
	}
	var out []Section
	if strings.TrimSpace(p.PrePrompt) != "" {
		out = append(out, Section{
			Anchor:  "cli",
			Path:    "(--pre-prompt)",
			Slot:    "cli_pre_prompt",
			Content: p.PrePrompt,
		})
	}
	out = append(out, sections...)
	if strings.TrimSpace(p.PostPrompt) != "" {
		out = append(out, Section{
			Anchor:  "cli",
			Path:    "(--post-prompt)",
			Slot:    "cli_post_prompt",
			Content: p.PostPrompt,
		})
	}
	return out, nil
}

// composeSections walks the Assembler-resolved file list, rendering
// framing fragments through the markdown engine and role_main through
// the factory. Result is the ordered Section list — wrappers are added
// by Resolve/Inspect.
func (p PromptFile) composeSections(ctx ResolveContext) ([]Section, error) {
	if ctx == nil {
		return nil, errors.New("prompts.PromptFile: nil ctx (need ResolveContext for Env and Vars)")
	}
	env := ctx.Env()
	if env == nil {
		return nil, errors.New("prompts.PromptFile: ctx.Env() returned nil")
	}
	if p.Path == "" {
		return nil, errors.New("prompts.PromptFile: empty Path")
	}

	a := p.Assembler
	if a == nil {
		a = env.Assembler()
		// Backward-compat shim: a `.prompt.md` path that looks like a
		// filesystem reference (contains a separator or starts with ".")
		// auto-wraps in a TempAnchorAssembler. Production cmd-layer
		// dispatch (`ateam exec @PATH`) already injects this explicitly;
		// the shim covers direct callers (tests, embedded use) that
		// construct a bare PromptFile. TempAnchor.Resolve handles the
		// `.prompt.md` → role basename conversion itself.
		if filesystemPromptPath(p.Path) {
			parentDir := filepath.Dir(p.Path)
			if parentDir == "" {
				parentDir = "."
			}
			if strings.TrimSuffix(filepath.Base(p.Path), ".prompt.md") == "" {
				return nil, errors.New("prompts.PromptFile: filesystem-path with empty role basename")
			}
			a = assembler.NewTempAnchor(parentDir, a)
		}
	}

	// Engine bound to the assembler's anchor chain — used for framing
	// fragments and (default-factory case) for role_main. Synthesizing
	// MultiAnchor from a.Anchors() works for every shipped Assembler
	// impl: MultiAnchor returns its own chain; TempAnchor returns the
	// external+inner chain; Basic returns nil (no includes, OK for
	// single-file path).
	engine := assembler.NewEngine(assembler.New(a.Anchors()), 0)
	if dyn := ctx.Dynamics(); dyn != nil {
		engine = engine.WithDispatcher(NewDispatcher(dyn, ctx))
	}
	vars := ctx.Vars()
	fac := p.Factory

	// CustomBody short-circuit: walk framing only, render the
	// operator-supplied body as role_main inline. Mirrors the legacy
	// AssembleOptions.ReplaceRoleMain path; the role_main file (if it
	// even exists) is intentionally bypassed.
	if p.CustomBody != "" {
		framing, err := a.ResolveFramingOnly(p.Path)
		if err != nil {
			return nil, err
		}
		preSecs, postSecs, err := renderFraming(framing, engine, vars)
		if err != nil {
			return nil, err
		}
		rendered, err := renderCustomBody(engine, vars, p.CustomBody)
		if err != nil {
			return nil, err
		}
		if rendered == "" {
			return nil, errors.New("role main override (PromptFile.CustomBody) is empty after rendering — provide non-whitespace content or omit the override")
		}
		out := make([]Section, 0, len(preSecs)+1+len(postSecs))
		out = append(out, preSecs...)
		out = append(out, Section{
			Anchor:  "cli",
			Path:    "(--prompt)",
			Slot:    assembler.SlotRoleMain,
			Content: rendered,
		})
		out = append(out, postSecs...)
		return out, nil
	}

	files, err := a.Resolve(p.Path)
	if err != nil {
		return nil, err
	}

	var sections []Section
	for _, f := range files {
		body, err := readFile(f.FS, f.Path)
		if err != nil {
			return nil, err
		}
		_, parsedBody, err := assembler.ParseFrontmatter(body)
		if err != nil {
			return nil, wrapFrontmatterErr(f, err)
		}
		var rendered string
		if f.Slot == assembler.SlotRoleMain {
			rendered, err = renderRoleMain(fac, engine, vars, f.Path, parsedBody, ctx)
		} else {
			rendered, err = engine.Render(parsedBody, vars)
		}
		if err != nil {
			return nil, wrapRenderErr(f, err)
		}
		if f.Slot == assembler.SlotRoleMain {
			if strings.TrimSpace(rendered) == "" {
				return nil, fmt.Errorf("role main at %s:%s is empty after rendering — the file exists but contains only whitespace, which would silently shadow any embedded fallback; remove the file or add a body", f.Anchor, f.Path)
			}
		} else if strings.TrimSpace(rendered) == "" {
			continue
		}
		sections = append(sections, Section{
			Anchor:  f.Anchor,
			Path:    f.Path,
			Slot:    f.Slot,
			Content: rendered,
		})
	}
	return sections, nil
}

// renderFraming renders a flat framing-fragment list (no role_main),
// splitting the result into pre-main and post-main groups by slot
// prefix so the orchestrator can insert role_main between them.
func renderFraming(files []assembler.ResolvedFile, engine *assembler.Engine, vars Vars) (pre, post []Section, err error) {
	for _, f := range files {
		body, rerr := readFile(f.FS, f.Path)
		if rerr != nil {
			return nil, nil, rerr
		}
		_, parsedBody, ferr := assembler.ParseFrontmatter(body)
		if ferr != nil {
			return nil, nil, wrapFrontmatterErr(f, ferr)
		}
		rendered, rerr := engine.Render(parsedBody, vars)
		if rerr != nil {
			return nil, nil, wrapRenderErr(f, rerr)
		}
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		sec := Section{Anchor: f.Anchor, Path: f.Path, Slot: f.Slot, Content: rendered}
		if isPostSlot(f.Slot) {
			post = append(post, sec)
		} else {
			pre = append(pre, sec)
		}
	}
	return pre, post, nil
}

func isPostSlot(slot string) bool {
	return slot == "role_post" || slot == "root_post" || strings.HasPrefix(slot, assembler.SlotDirPost+":")
}

// renderRoleMain delegates to the Factory when set; otherwise renders
// through the orchestrator's own engine so {{include}} and friends
// still work against the assembler chain (test invariant). `body`
// arrives already stripped of frontmatter.
func renderRoleMain(fac PromptFactory, engine *assembler.Engine, vars Vars, path, body string, ctx ResolveContext) (string, error) {
	if fac == nil {
		return engine.Render(body, vars)
	}
	// Explicit factory: let the impl take over. The default
	// PromptFactory ships PromptText which drops include support — that
	// trade-off is documented; non-default factories (PythonPrompt etc.)
	// own their own rendering strategy entirely.
	return fac.For(path, body).Resolve(ctx)
}

// renderCustomBody runs the operator-supplied role_main override through
// the engine. Mirrors the legacy assembler.AssembleOptions.ReplaceRoleMain
// path; empty result is reported as an error by the caller.
func renderCustomBody(engine *assembler.Engine, vars Vars, body string) (string, error) {
	rendered, err := engine.Render(body, vars)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rendered) == "" {
		return "", nil
	}
	return rendered, nil
}

func readFile(fsys fs.FS, path string) (string, error) {
	if fsys == nil {
		return "", fmt.Errorf("read %s: FS is nil", path)
	}
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(data), nil
}

func wrapFrontmatterErr(f assembler.ResolvedFile, err error) error {
	loc := f.Path
	if f.Anchor != "" {
		loc = f.Anchor + ":" + f.Path
	}
	return fmt.Errorf("frontmatter %s: %w", loc, err)
}

func wrapRenderErr(f assembler.ResolvedFile, err error) error {
	loc := f.Path
	if f.Anchor != "" {
		loc = f.Anchor + ":" + f.Path
	}
	return fmt.Errorf("rendering %s: %w", loc, err)
}

func joinSections(sections []Section) string {
	if len(sections) == 0 {
		return ""
	}
	parts := make([]string, len(sections))
	for i, s := range sections {
		parts[i] = s.Content
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// wrapRaw appends pre and post around body as RAW text (no engine). The
// `\n\n---\n\n` separator matches the assembler's join — keeping the
// shape consistent with the framing sections inside. Whitespace-only
// values drop silently so an empty --pre-prompt doesn't add a blank
// separator.
func wrapRaw(pre, body, post string) string {
	prePart := strings.TrimSpace(pre)
	postPart := strings.TrimSpace(post)
	parts := make([]string, 0, 3)
	if prePart != "" {
		parts = append(parts, pre)
	}
	if body != "" {
		parts = append(parts, body)
	}
	if postPart != "" {
		parts = append(parts, post)
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// renderWithCtx runs the assembler engine against ctx — vars + dynamics
// wired up — using the supplied anchor list (nil for inline-text impls
// that don't support includes).
func renderWithCtx(a *assembler.MultiAnchorAssembler, text string, ctx ResolveContext) (string, error) {
	e := assembler.NewEngine(a, 0)
	if ctx == nil {
		return e.Render(text, nil)
	}
	if dyn := ctx.Dynamics(); dyn != nil {
		e = e.WithDispatcher(NewDispatcher(dyn, ctx))
	}
	return e.Render(text, ctx.Vars())
}
