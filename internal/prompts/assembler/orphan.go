package assembler

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// OrphanError describes a role fragment (pre/post) whose base role has no
// matching `<role>.prompt.md` anywhere in the anchor chain.
type OrphanError struct {
	Anchor string // which anchor owns this orphan
	Path   string // path within the anchor's FS
	Role   string // role name parsed from the filename
	Dir    string // directory holding the file (relative to anchor root)
	Hint   string // closest known role in the same directory, if any
}

func (e *OrphanError) Error() string {
	loc := e.Path
	if e.Anchor != "" {
		loc = e.Anchor + ":" + e.Path
	}
	msg := fmt.Sprintf("orphan fragment: %s\n  no matching %s.prompt.md found in any anchor", loc, e.Role)
	if e.Hint != "" {
		msg += "\n  did you mean: " + e.Hint + "?"
	}
	return msg
}

// FindOrphans walks every anchor, classifies each `.md` file, and returns one
// OrphanError per role pre/post fragment whose role has no matching
// `<role>.prompt.md` somewhere in the chain.
//
// Matching is per-directory: `report/security.pre.scope.md` pairs only with
// `report/security.prompt.md`, not `code/security.prompt.md`.
//
// Files classified as Unknown (arbitrary includes) and dir-level
// `_pre.*.md` / `_post.*.md` are never orphans.
func (a *Assembler) FindOrphans() ([]*OrphanError, error) {
	knownByDir := make(map[string]map[string]bool)

	type fragmentEntry struct {
		anchor string
		path   string
		dir    string
		role   string
	}
	var fragments []fragmentEntry

	for _, anc := range a.anchors {
		err := fs.WalkDir(anc.FS, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				// A missing prompts/ subtree means this anchor simply has no
				// fragments — not an error (mirrors BuildAnchors' handling).
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			base := d.Name()
			if !strings.HasSuffix(base, ".md") {
				return nil
			}
			parsed := Parse(base)
			dir := path.Dir(p)
			if dir == "." {
				dir = ""
			}
			switch parsed.Kind {
			case KindRoleMain:
				set := knownByDir[dir]
				if set == nil {
					set = make(map[string]bool)
					knownByDir[dir] = set
				}
				set[parsed.Role] = true
			case KindRolePre, KindRolePost:
				fragments = append(fragments, fragmentEntry{
					anchor: anc.Name,
					path:   p,
					dir:    dir,
					role:   parsed.Role,
				})
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk anchor %s: %w", anc.Name, err)
		}
	}

	var orphans []*OrphanError
	for _, f := range fragments {
		if knownByDir[f.dir][f.role] {
			continue
		}
		hint := closestRole(f.role, sortedKeys(knownByDir[f.dir]))
		orphans = append(orphans, &OrphanError{
			Anchor: f.anchor,
			Path:   f.path,
			Role:   f.role,
			Dir:    f.dir,
			Hint:   hint,
		})
	}

	sort.Slice(orphans, func(i, j int) bool {
		if orphans[i].Anchor != orphans[j].Anchor {
			return orphans[i].Anchor < orphans[j].Anchor
		}
		return orphans[i].Path < orphans[j].Path
	})
	return orphans, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// closestRole returns the candidate within edit distance ≤ len(target)/2 + 1
// closest to target, or "" if nothing is close enough. The threshold keeps
// suggestions useful (a single typo) and avoids comically wrong hints.
func closestRole(target string, candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	threshold := len(target)/2 + 1
	best := ""
	bestDist := -1
	for _, c := range candidates {
		d := levenshtein(target, c)
		if d > threshold {
			continue
		}
		if bestDist < 0 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
