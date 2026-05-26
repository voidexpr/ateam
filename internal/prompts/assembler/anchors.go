package assembler

import (
	"io/fs"
	"os"
)

// PromptsSubdir is the subdirectory within each anchor root that holds the
// v1 prompt tree. The Assembler resolves paths relative to this subdir, so
// callers reference files as `report/security.prompt.md`, `_pre.context.md`,
// etc. — never with a leading `prompts/` segment.
const PromptsSubdir = "prompts"

// BuildAnchors returns the standard three-anchor resolution chain in the
// order Assembler expects (most-specific first):
//
//  1. project — <projectDir>/prompts/
//  2. org     — <orgDir>/prompts/ (omitted when orgDir is empty)
//  3. embedded — the prompts/ subtree of `embedded` (the embed.FS for
//     built-in defaults)
//
// Either filesystem anchor (project / org) gracefully handles a missing
// `prompts/` subdir: reads simply return fs.ErrNotExist, which the
// Assembler treats as "anchor doesn't have this file" rather than an error.
// This lets the function be called against any project — even one whose
// prompt tree hasn't been populated yet.
func BuildAnchors(projectDir, orgDir string, embedded fs.FS) []Anchor {
	anchors := make([]Anchor, 0, 3)
	if projectDir != "" {
		anchors = append(anchors, Anchor{
			Name: "project",
			FS:   os.DirFS(joinPrompts(projectDir)),
		})
	}
	if orgDir != "" {
		anchors = append(anchors, Anchor{
			Name: "org",
			FS:   os.DirFS(joinPrompts(orgDir)),
		})
	}
	if embedded != nil {
		// fs.Sub returns a "rooted" FS even if the subdir is absent at the
		// time of construction — reads against it return fs.ErrNotExist
		// which the Assembler swallows. That keeps the embedded anchor
		// usable during the transition before defaults/prompts/ is
		// populated.
		subFS, err := fs.Sub(embedded, PromptsSubdir)
		if err == nil {
			anchors = append(anchors, Anchor{Name: "embedded", FS: subFS})
		}
	}
	return anchors
}

func joinPrompts(root string) string {
	// Built without filepath.Join to keep this dep-light. The caller is
	// expected to pass a clean absolute path; os.DirFS handles a missing
	// directory by returning fs.ErrNotExist on reads.
	if root == "" {
		return PromptsSubdir
	}
	if root[len(root)-1] == '/' {
		return root + PromptsSubdir
	}
	return root + "/" + PromptsSubdir
}
