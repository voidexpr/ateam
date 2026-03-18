package config

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

const maxProjectIDLen = 240

// PathToProjectID converts a relative project path to a safe directory name.
// Rules: _ → __, / → _. Does not handle "." or ".." components;
// callers creating new projects should call ValidateProjectPath first.
// Paths longer than 240 chars after encoding are truncated with a hash suffix.
func PathToProjectID(relPath string) string {
	if relPath == "" {
		return ""
	}

	var b strings.Builder
	for _, c := range relPath {
		switch c {
		case '_':
			b.WriteString("__")
		case '/':
			b.WriteByte('_')
		default:
			b.WriteRune(c)
		}
	}

	key := b.String()
	if len(key) > maxProjectIDLen {
		h := sha256.Sum256([]byte(relPath))
		suffix := fmt.Sprintf("_%x", h[:4])
		key = key[:maxProjectIDLen-len(suffix)] + suffix
	}
	return key
}

// ProjectIDToPath reverses PathToProjectID for non-truncated IDs.
// __ → _, single _ → /. Truncated IDs (with hash suffix) produce a
// best-effort result; callers should verify the path exists.
func ProjectIDToPath(id string) string {
	if id == "" {
		return ""
	}
	const placeholder = "\x00"
	s := strings.ReplaceAll(id, "__", placeholder)
	s = strings.ReplaceAll(s, "_", "/")
	s = strings.ReplaceAll(s, placeholder, "_")
	return s
}

// ValidateProjectPath rejects paths that are "." or ".." or contain "." or ".."
// as a path component (which would create hidden or parent-traversal directories).
func ValidateProjectPath(relPath string) error {
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		if part == "." || part == ".." {
			return fmt.Errorf("invalid project path %q: contains %q component", relPath, part)
		}
	}
	return nil
}
