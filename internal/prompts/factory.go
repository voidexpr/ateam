package prompts

// PromptFactory picks the Prompt implementation for a role-main file.
// Framing fragments (slots != "role_main") are always rendered by the
// markdown engine — they're documented to be markdown. Only the role
// main can vary.
//
// Shipped today: DefaultPromptFactory returns PromptText{Text: <body>}
// for every path. Future extension-mapping factories register entries
// for `.prompt.py`, `.prompt.jinja`, etc., and fall through to the
// default for paths they don't recognize.
type PromptFactory interface {
	// For returns the Prompt impl that renders the file at `path`
	// using `body` as the file's already-read content. `body` is the
	// raw file contents; impls that don't pre-bind body can read it
	// themselves through the ResolveContext they receive at Resolve
	// time.
	For(path, body string) Prompt
}

// defaultFactory returns PromptText{Text: body} for every path. The
// PromptText impl runs the body through the markdown engine
// (vars + dynamics) when Resolve is called — same shape as today's
// role-main rendering inside Assemble.
type defaultFactory struct{}

func (defaultFactory) For(_ string, body string) Prompt {
	return PromptText{Text: body}
}

// DefaultPromptFactory returns the no-op factory shipped today: every
// path maps to PromptText. Reserved as a function (not a package
// variable) so future extension factories can compose it via
// fall-through without taking a global lock.
func DefaultPromptFactory() PromptFactory { return defaultFactory{} }
