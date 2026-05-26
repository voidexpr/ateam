// Package assembler implements the filename-driven prompt composition
// described in plans/Feature_prompt_report_fs_refactor.md.
//
// Phase A scope: pure data types — filename pattern parsing, role-name
// validation, anchor-based file resolution. No template engine, no
// frontmatter, no callers wired yet.
package assembler

import "strings"

// FileKind classifies a prompt file by its filename suffix pattern.
type FileKind int

const (
	// KindUnknown is any *.md file that doesn't match a recognized pattern.
	// The framework parser ignores it; it can still be referenced explicitly
	// via {{include}}.
	KindUnknown FileKind = iota
	// KindRoleMain is <role>.prompt.md — the role's main body.
	KindRoleMain
	// KindRolePre is <role>.pre.md or <role>.pre.<NAME>.md — role-level pre fragment.
	KindRolePre
	// KindRolePost is <role>.post.md or <role>.post.<NAME>.md — role-level post fragment.
	KindRolePost
	// KindDirPre is _pre.md or _pre.<NAME>.md — dir-level pre.
	KindDirPre
	// KindDirPost is _post.md or _post.<NAME>.md — dir-level post.
	KindDirPost
)

// Parsed is the result of classifying a filename.
type Parsed struct {
	Kind     FileKind
	Role     string // empty for dir-level and KindUnknown
	Fragment string // <NAME> from .pre.<NAME>/.post.<NAME>; empty for the singleton form
}

// Parse classifies a single filename (no directory part) according to the
// suffix-anchored rules in the spec.
//
// Files starting with `_` are routed to the dir-level branch; everything else
// is role-level. For role pre/post, the rightmost `.pre` or `.post` separator
// wins ("role = everything before the final `.pre`"). Whatever follows the
// separator (after the dot) becomes the fragment name; an absent fragment
// means the singleton form.
//
// Role-name restrictions (no leading `_`, no trailing `.pre`/`.post`) are
// NOT enforced here — validation runs separately after parsing so callers
// can produce better error messages with the originating file path.
func Parse(filename string) Parsed {
	if !strings.HasSuffix(filename, ".md") {
		return Parsed{Kind: KindUnknown}
	}
	body := strings.TrimSuffix(filename, ".md")
	if body == "" {
		return Parsed{Kind: KindUnknown}
	}

	if strings.HasPrefix(filename, "_") {
		return parseDirLevel(body)
	}
	return parseRoleLevel(body)
}

func parseDirLevel(body string) Parsed {
	switch {
	case body == "_pre":
		return Parsed{Kind: KindDirPre}
	case body == "_post":
		return Parsed{Kind: KindDirPost}
	case strings.HasPrefix(body, "_pre."):
		name := body[len("_pre."):]
		if name == "" {
			return Parsed{Kind: KindUnknown}
		}
		return Parsed{Kind: KindDirPre, Fragment: name}
	case strings.HasPrefix(body, "_post."):
		name := body[len("_post."):]
		if name == "" {
			return Parsed{Kind: KindUnknown}
		}
		return Parsed{Kind: KindDirPost, Fragment: name}
	}
	return Parsed{Kind: KindUnknown}
}

func parseRoleLevel(body string) Parsed {
	if strings.HasSuffix(body, ".prompt") {
		role := strings.TrimSuffix(body, ".prompt")
		if role == "" {
			return Parsed{Kind: KindUnknown}
		}
		return Parsed{Kind: KindRoleMain, Role: role}
	}
	if role, frag, ok := splitMarker(body, "pre"); ok {
		return Parsed{Kind: KindRolePre, Role: role, Fragment: frag}
	}
	if role, frag, ok := splitMarker(body, "post"); ok {
		return Parsed{Kind: KindRolePost, Role: role, Fragment: frag}
	}
	return Parsed{Kind: KindUnknown}
}

// splitMarker finds the rightmost occurrence of `.<marker>` in body such that
// what follows is either empty (singleton form `<role>.<marker>`) or starts
// with a dot (fragment form `<role>.<marker>.<NAME>`). Returns role, fragment,
// and ok. A bare `.<marker>foo` suffix (e.g. `.preamble`) is not a marker; the
// scan walks left to find an earlier valid position.
func splitMarker(body, marker string) (role, frag string, ok bool) {
	needle := "." + marker
	search := body
	for {
		idx := strings.LastIndex(search, needle)
		if idx <= 0 {
			return "", "", false
		}
		after := body[idx+len(needle):]
		if after == "" {
			return body[:idx], "", true
		}
		if strings.HasPrefix(after, ".") {
			name := after[1:]
			if name == "" {
				return "", "", false
			}
			return body[:idx], name, true
		}
		// `.markerfoo` — not a separator; keep looking further left.
		search = body[:idx]
	}
}
