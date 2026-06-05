package assembler

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// ErrMissingRoleMain wraps the "no <role>.prompt.md in any anchor" error
// MultiAnchorAssembler.Resolve returns. Orchestrators that supply
// CustomBody can detect and skip; other callers surface the wrapped
// message as-is.
var ErrMissingRoleMain = errors.New("role main missing")

// Resolve walks the anchor chain and returns the ordered ResolvedFile
// list — pre framing, role main, post framing — that composes the
// prompt at `name`, WITHOUT rendering. Errors when no <role>.prompt.md
// is present in any anchor; callers who want framing-only behavior
// (e.g. CustomBody is set) call ResolveFramingOnly.
func (a *MultiAnchorAssembler) Resolve(name string) ([]ResolvedFile, error) {
	parsed, err := parseAssemblyName(name)
	if err != nil {
		return nil, err
	}
	pre, err := a.framingPre(parsed)
	if err != nil {
		return nil, err
	}
	main, err := a.resolveRoleMain(parsed)
	if err != nil {
		return nil, err
	}
	post, err := a.framingPost(parsed)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedFile, 0, len(pre)+1+len(post))
	out = append(out, pre...)
	out = append(out, main)
	out = append(out, post...)
	return out, nil
}

// ResolveFramingOnly returns the pre + post framing fragments around
// `name` without touching the role_main file. Never errors on missing
// main.
func (a *MultiAnchorAssembler) ResolveFramingOnly(name string) ([]ResolvedFile, error) {
	parsed, err := parseAssemblyName(name)
	if err != nil {
		return nil, err
	}
	pre, err := a.framingPre(parsed)
	if err != nil {
		return nil, err
	}
	post, err := a.framingPost(parsed)
	if err != nil {
		return nil, err
	}
	return append(pre, post...), nil
}

type assemblyName struct {
	role string
	dirs []string
}

func parseAssemblyName(name string) (assemblyName, error) {
	name = strings.TrimSuffix(name, ".prompt.md")
	name = strings.Trim(name, "/")
	if name == "" {
		return assemblyName{}, fmt.Errorf("empty prompt path")
	}
	parts := strings.Split(name, "/")
	return assemblyName{
		role: parts[len(parts)-1],
		dirs: parts[:len(parts)-1],
	}, nil
}

func (a *MultiAnchorAssembler) framingPre(n assemblyName) ([]ResolvedFile, error) {
	fsByAnchor := a.fsByAnchor()
	var out []ResolvedFile
	// 1. Dir-level pres, root → leaf.
	for i := 0; i <= len(n.dirs); i++ {
		dir := strings.Join(n.dirs[:i], "/")
		matches, err := a.AllMatches(fragmentGlobs(dir, "_pre")...)
		if err != nil {
			return nil, fmt.Errorf("glob dir pre %q: %w", dir, err)
		}
		slot := SlotRootPre
		if dir != "" {
			slot = SlotDirPre + ":" + dir
		}
		out = append(out, matchesToFiles(slot, matches, fsByAnchor)...)
	}
	// 2. Role-level pres.
	dir := strings.Join(n.dirs, "/")
	rolePres, err := a.AllMatches(fragmentGlobs(dir, n.role+".pre")...)
	if err != nil {
		return nil, fmt.Errorf("glob role pre %q: %w", joinName(dir, n.role), err)
	}
	out = append(out, matchesToFiles("role_pre", rolePres, fsByAnchor)...)
	return out, nil
}

func (a *MultiAnchorAssembler) framingPost(n assemblyName) ([]ResolvedFile, error) {
	fsByAnchor := a.fsByAnchor()
	var out []ResolvedFile
	// 4. Role-level posts.
	dir := strings.Join(n.dirs, "/")
	rolePosts, err := a.AllMatches(fragmentGlobs(dir, n.role+".post")...)
	if err != nil {
		return nil, fmt.Errorf("glob role post %q: %w", joinName(dir, n.role), err)
	}
	out = append(out, matchesToFiles("role_post", rolePosts, fsByAnchor)...)
	// 5. Dir-level posts, leaf → root.
	for i := len(n.dirs); i >= 0; i-- {
		d := strings.Join(n.dirs[:i], "/")
		matches, err := a.AllMatches(fragmentGlobs(d, "_post")...)
		if err != nil {
			return nil, fmt.Errorf("glob dir post %q: %w", d, err)
		}
		slot := "root_post"
		if d != "" {
			slot = SlotDirPost + ":" + d
		}
		out = append(out, matchesToFiles(slot, matches, fsByAnchor)...)
	}
	return out, nil
}

func (a *MultiAnchorAssembler) resolveRoleMain(n assemblyName) (ResolvedFile, error) {
	dir := strings.Join(n.dirs, "/")
	mainPath := joinName(dir, n.role+".prompt.md")
	main, ok, err := a.FirstMatch(mainPath)
	if err != nil {
		return ResolvedFile{}, fmt.Errorf("lookup %q: %w", mainPath, err)
	}
	if !ok {
		return ResolvedFile{}, fmt.Errorf("no role main at %s in any anchor: %w", mainPath, ErrMissingRoleMain)
	}
	return ResolvedFile{
		Slot:   SlotRoleMain,
		Anchor: main.Anchor,
		Path:   main.Path,
		FS:     a.fsByAnchor()[main.Anchor],
	}, nil
}

// fsByAnchor maps Anchor.Name to its FS so ResolvedFile carries the
// right FS handle for content reads.
func (a *MultiAnchorAssembler) fsByAnchor() map[string]fs.FS {
	out := make(map[string]fs.FS, len(a.anchors))
	for _, anc := range a.anchors {
		out[anc.Name] = anc.FS
	}
	return out
}

// matchesToFiles turns []Match into []ResolvedFile with a given slot,
// stamping FS by anchor name.
func matchesToFiles(slot string, matches []Match, fsByAnchor map[string]fs.FS) []ResolvedFile {
	out := make([]ResolvedFile, 0, len(matches))
	for _, m := range matches {
		out = append(out, ResolvedFile{
			Slot:   slot,
			Anchor: m.Anchor,
			Path:   m.Path,
			FS:     fsByAnchor[m.Anchor],
		})
	}
	return out
}
