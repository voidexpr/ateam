package assembler

import (
	"fmt"
	"strings"
)

// Frontmatter holds the recognized keys from a prompt's YAML frontmatter.
//
// v1 allow-list — these are ateam-internal metadata, not a user-pluggable
// extension surface. Unknown keys error at parse time so typos surface loudly
// and so the surface stays available for future ateam-internal additions
// without colliding with ad-hoc user keys.
type Frontmatter struct {
	Description string
	Deprecated  bool
	Legacy      bool
}

// AllowedFrontmatterKeys is the closed set of recognized keys.
var AllowedFrontmatterKeys = []string{"description", "deprecated", "legacy"}

// ParseFrontmatter extracts the YAML frontmatter block (lines between two
// `---` separators at the start of the file) and returns it parsed plus the
// body after the closing separator. When no frontmatter is present, returns
// zero-value Frontmatter and content unchanged.
//
// Strict allow-list: any key not in AllowedFrontmatterKeys errors with the
// offending key in the message. Malformed frontmatter (missing closing `---`,
// non-key:value lines, invalid bool values) also errors.
func ParseFrontmatter(content string) (Frontmatter, string, error) {
	// Normalize CRLF up front so detection and the body return are line-ending
	// agnostic. Without this a Windows-authored file starting "---\r\n" fails
	// the LF-only prefix check and leaks the whole frontmatter block verbatim
	// into the rendered prompt.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return Frontmatter{}, content, nil
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		// Allow `---` as the very last line (no trailing newline).
		if strings.HasSuffix(rest, "\n---") {
			end = len(rest) - 4
		} else {
			return Frontmatter{}, content, fmt.Errorf("frontmatter: opening `---` has no closing `---`")
		}
	}
	block := rest[:end]
	var body string
	if end+5 <= len(rest) {
		body = strings.TrimLeft(rest[end+5:], "\n")
	}

	var fm Frontmatter
	for lineNo, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.IndexByte(trimmed, ':')
		if colon <= 0 {
			return Frontmatter{}, content, fmt.Errorf("frontmatter line %d: expected `key: value`, got %q", lineNo+1, trimmed)
		}
		key := strings.TrimSpace(trimmed[:colon])
		val := unquoteValue(strings.TrimSpace(trimmed[colon+1:]))

		switch key {
		case "description":
			fm.Description = val
		case "deprecated":
			b, err := parseBool(val)
			if err != nil {
				return Frontmatter{}, content, fmt.Errorf("frontmatter `deprecated`: %w", err)
			}
			fm.Deprecated = b
		case "legacy":
			b, err := parseBool(val)
			if err != nil {
				return Frontmatter{}, content, fmt.Errorf("frontmatter `legacy`: %w", err)
			}
			fm.Legacy = b
		default:
			return Frontmatter{}, content, fmt.Errorf("frontmatter: unknown key %q (allowed: %s)", key, strings.Join(AllowedFrontmatterKeys, ", "))
		}
	}
	return fm, body, nil
}

// unquoteValue strips a single balanced pair of surrounding double quotes.
// Unlike strings.Trim(s, `"`), it leaves values whose first/last rune merely
// happens to be a quote intact: `say "hi"` stays `say "hi"`, while a fully
// wrapped `"hi"` unwraps to `hi`.
func unquoteValue(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func parseBool(s string) (bool, error) {
	switch s {
	case "true", "True", "TRUE", "yes", "Yes":
		return true, nil
	case "false", "False", "FALSE", "no", "No", "":
		return false, nil
	}
	return false, fmt.Errorf("invalid bool %q (want true/false)", s)
}
