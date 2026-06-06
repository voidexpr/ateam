package prompts

// PromptFactory picks the Prompt implementation for a role-main file.
// Framing fragments (slots != "role_main") are always rendered by the
// markdown engine — they're documented to be markdown. Only the role
// main can vary.
//
// PromptFile.Resolve uses Factory when set; nil Factory falls through
// to the orchestrator's own engine (preserving {{include}} resolution
// against the anchor chain). Future extension-mapping factories ship
// when concrete consumers arrive (e.g. `.prompt.py` → PythonPrompt,
// `.prompt.jinja` → JinjaPrompt).
//
// No default impl is shipped: a "default factory" would have to return
// some Prompt whose Resolve diverges from the nil-Factory path —
// PromptText drops include support, an orchestrator-engine-bound impl
// requires plumbing the engine into Prompt construction. Either choice
// is a foot-gun. Leaving Factory nil is the supported way to say "use
// the default behavior".
type PromptFactory interface {
	// For returns the Prompt impl that renders the file at `path`
	// using `body` as the file's already-read content. `body` is the
	// raw file contents (frontmatter stripped); impls that need to
	// re-read the file via ctx.Env().Assembler() may do so.
	For(path, body string) Prompt
}
