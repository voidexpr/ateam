// Package projectinfo collects a small set of generic, language- and
// build-system-agnostic facts about a git repository.
//
// The intent is to provide a stable API that callers can use to enrich
// role prompts with mechanical orientation (top-level layout, recent
// commits, detected manifests) without each role re-deriving it via
// ls / find / wc / git log. The package depends only on git; no
// language-specific tools are invoked. See plans/Feature_TokenReduction.md
// (Phase 0.5) for the design context.
//
// This is the "Layer A — Universal core" portion only. Stack-specific
// profile blocks (Go, Node, Python, Rust, etc.) will land in a separate
// package on top of this API.
package projectinfo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ateam/internal/gitutil"
)

// Info holds the universal-core orientation facts for a project directory.
// All fields are best-effort: a missing git binary, a non-repo directory,
// or transient git failures degrade gracefully to zero values without
// returning an error. The only hard failure mode is the directory not
// existing or not being readable.
type Info struct {
	// Dir is the directory the collection was run against, after resolving
	// symlinks. For a git repo this is the git toplevel; for a non-repo
	// directory it's the input dir resolved to an absolute path.
	Dir string `json:"dir"`

	// GitRepo is true when Dir is inside a git working tree.
	GitRepo bool `json:"git_repo"`

	// Branch is the current branch name, or "" if not on a branch
	// (detached HEAD) or git is unavailable.
	Branch string `json:"branch,omitempty"`

	// HeadHash is the full 40-char SHA of HEAD, or "" if unavailable.
	HeadHash string `json:"head_hash,omitempty"`

	// HeadDate is the commit time of HEAD formatted as YYYY-MM-DD_HH-MM-SS,
	// or "" if unavailable.
	HeadDate string `json:"head_date,omitempty"`

	// HeadSubject is the first line of the HEAD commit message, or ""
	// if unavailable.
	HeadSubject string `json:"head_subject,omitempty"`

	// WorkingTreeStatus is "clean" when there are no uncommitted changes,
	// "dirty (N files)" when there are, or "unknown" when git is unavailable.
	WorkingTreeStatus string `json:"working_tree_status"`

	// UncommittedFiles lists the porcelain status lines (one per file) when
	// the working tree is dirty. Empty when clean or git is unavailable.
	UncommittedFiles []string `json:"uncommitted_files,omitempty"`

	// TopLevelEntries lists names of entries directly under Dir, including
	// directories. Hidden entries (starting with ".") are excluded except
	// for a small allowlist (.github, .gitlab, .ateam) that commonly
	// carries load-bearing project metadata.
	TopLevelEntries []string `json:"top_level_entries"`

	// TrackedFileCount is the total number of files tracked by git, or 0
	// when git is unavailable.
	TrackedFileCount int `json:"tracked_file_count"`

	// RecentCommits is the most recent N commits (default 10) on the
	// current branch, oldest last.
	RecentCommits []Commit `json:"recent_commits"`

	// DocsAtRoot lists documentation files (.md/.rst/.adoc/.txt) directly
	// under Dir. Sorted alphabetically.
	DocsAtRoot []string `json:"docs_at_root"`

	// Manifests lists detected build / package / project manifest files
	// found at Dir. Used by downstream profile detection.
	Manifests []string `json:"manifests"`
}

// Commit is a single entry in RecentCommits.
type Commit struct {
	ShortSHA string `json:"short_sha"`
	Subject  string `json:"subject"`
}

// knownManifests is the allowlist of project-manifest file names that
// indicate a particular stack or build system. The list is intentionally
// generous; presence detection is cheap and consumers filter further.
var knownManifests = []string{
	// Go
	"go.mod",
	// Node / JavaScript / TypeScript
	"package.json", "deno.json", "deno.jsonc", "bun.lockb",
	// Rust
	"Cargo.toml",
	// Python
	"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt",
	// Ruby
	"Gemfile",
	// Elixir
	"mix.exs",
	// JVM
	"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "pom.xml",
	// PHP
	"composer.json",
	// C / C++ / native
	"CMakeLists.txt", "Makefile.am", "configure.ac", "meson.build",
	// Zig
	"build.zig",
	// Swift
	"Package.swift",
	// .NET
	"*.csproj", "*.sln", // glob handling below
	// Generic build runners
	"Makefile", "justfile", "Justfile",
	"Taskfile.yml", "Taskfile.yaml",
	"Dockerfile", "docker-compose.yml", "docker-compose.yaml",
}

// hiddenAllowlist lists dot-prefixed top-level entries that are commonly
// load-bearing and should appear in TopLevelEntries even though they
// otherwise look like hidden config.
var hiddenAllowlist = map[string]struct{}{
	".github":       {},
	".gitlab":       {},
	".gitea":        {},
	".ateam":        {},
	".devcontainer": {},
	".vscode":       {},
	".idea":         {},
	".circleci":     {},
	".cargo":        {},
	".config":       {},
	".ci":           {},
}

// docExts is the set of file extensions that count as "documentation"
// at the project root. Case-insensitive.
var docExts = map[string]struct{}{
	".md":   {},
	".rst":  {},
	".adoc": {},
	".txt":  {},
}

// recentCommitsDefault is the default count returned by Collect.
const recentCommitsDefault = 10

// Collect gathers universal-core facts about dir. The directory must
// exist and be readable. All git-derived fields degrade gracefully when
// git is unavailable or dir is not a repo.
//
// Callers that already hold a fresh ProjectMeta for dir should use
// CollectWithMeta to avoid forking `git log` + `git status` a second time.
func Collect(dir string) (*Info, error) {
	return collectFrom(dir, nil)
}

// CollectWithMeta is like Collect but reuses a pre-fetched ProjectMeta
// for HEAD facts (commit hash/date/subject, uncommitted files) instead
// of forking git again. Pass meta=nil to fall back to Collect's behavior.
func CollectWithMeta(dir string, meta *gitutil.ProjectMeta) (*Info, error) {
	return collectFrom(dir, meta)
}

func collectFrom(dir string, meta *gitutil.ProjectMeta) (*Info, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", dir, err)
	}
	if st, err := os.Stat(absDir); err != nil {
		return nil, fmt.Errorf("stat %q: %w", absDir, err)
	} else if !st.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", absDir)
	}

	info := &Info{Dir: absDir, WorkingTreeStatus: "unknown"}

	// Resolve to git toplevel when applicable. Falls back to the input
	// directory when not in a repo.
	if top := gitutil.TopLevel(absDir); top != "" {
		info.Dir = top
		info.GitRepo = true
	}

	info.collectTopLevelEntries()
	info.collectDocsAtRoot()
	info.collectManifests()

	if info.GitRepo {
		info.collectGitFacts(meta)
	}

	return info, nil
}

func (i *Info) collectTopLevelEntries() {
	entries, err := os.ReadDir(i.Dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			if _, ok := hiddenAllowlist[n]; !ok {
				continue
			}
		}
		names = append(names, n)
	}
	sort.Strings(names)
	i.TopLevelEntries = names
}

func (i *Info) collectDocsAtRoot() {
	entries, err := os.ReadDir(i.Dir)
	if err != nil {
		return
	}
	docs := make([]string, 0, 8)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if _, ok := docExts[ext]; ok {
			docs = append(docs, e.Name())
		}
	}
	sort.Strings(docs)
	i.DocsAtRoot = docs
}

func (i *Info) collectManifests() {
	// Build a set of literal names plus track which glob patterns to apply.
	literals := make(map[string]struct{}, len(knownManifests))
	var globPatterns []string
	for _, m := range knownManifests {
		if strings.ContainsAny(m, "*?[") {
			globPatterns = append(globPatterns, m)
		} else {
			literals[m] = struct{}{}
		}
	}

	entries, err := os.ReadDir(i.Dir)
	if err != nil {
		return
	}
	found := make([]string, 0, 4)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := literals[name]; ok {
			found = append(found, name)
			continue
		}
		for _, pat := range globPatterns {
			if ok, _ := filepath.Match(pat, name); ok {
				found = append(found, name)
				break
			}
		}
	}
	sort.Strings(found)
	i.Manifests = found
}

func (i *Info) collectGitFacts(meta *gitutil.ProjectMeta) {
	if meta == nil {
		meta, _ = gitutil.GetProjectMeta(i.Dir)
	}
	if meta != nil {
		i.HeadHash = meta.CommitHash
		i.HeadDate = meta.CommitDate
		i.HeadSubject = meta.CommitMessage
		if len(meta.Uncommitted) == 0 {
			i.WorkingTreeStatus = "clean"
		} else {
			i.WorkingTreeStatus = fmt.Sprintf("dirty (%d files)", len(meta.Uncommitted))
			i.UncommittedFiles = meta.Uncommitted
		}
	}

	i.Branch = runGit(i.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if i.Branch == "HEAD" {
		// Detached HEAD — leave Branch empty so consumers can distinguish.
		i.Branch = ""
	}

	if out := runGit(i.Dir, "ls-files"); out != "" {
		// runGit already TrimSpaces, so lines = (newline count) + 1.
		i.TrackedFileCount = strings.Count(out, "\n") + 1
	}

	i.RecentCommits = collectRecentCommits(i.Dir, recentCommitsDefault)
}

// collectRecentCommits returns up to n most-recent commits in
// newest-first order. Returns nil on any git failure.
func collectRecentCommits(dir string, n int) []Commit {
	out := runGit(dir, "log", fmt.Sprintf("-%d", n), "--pretty=%h %s")
	if out == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		idx := strings.IndexByte(line, ' ')
		if idx <= 0 {
			continue
		}
		commits = append(commits, Commit{
			ShortSHA: line[:idx],
			Subject:  line[idx+1:],
		})
	}
	return commits
}

// runGit executes git with the given args in dir, returning trimmed
// stdout on success or "" on any failure. Used for best-effort git
// calls where caller wants graceful degradation, not error propagation.
func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Markdown renders Info as a markdown block suitable for inclusion in a
// role prompt preamble. The format is stable and intended to be matched
// by golden tests in downstream callers.
func (i *Info) Markdown() string {
	var b strings.Builder

	b.WriteString("## Quick orientation (auto-generated; do not re-derive)\n\n")

	if !i.GitRepo {
		b.WriteString("Universal:\n")
		fmt.Fprintf(&b, "* Working directory: %s (not a git repository)\n", i.Dir)
		if len(i.TopLevelEntries) > 0 {
			fmt.Fprintf(&b, "* Top-level: %s\n", strings.Join(i.TopLevelEntries, ", "))
		}
		if len(i.DocsAtRoot) > 0 {
			fmt.Fprintf(&b, "* Docs at root: %s\n", strings.Join(i.DocsAtRoot, ", "))
		}
		if len(i.Manifests) > 0 {
			fmt.Fprintf(&b, "* Manifests detected: %s\n", strings.Join(i.Manifests, ", "))
		}
		return b.String()
	}

	b.WriteString("Universal:\n")
	if len(i.TopLevelEntries) > 0 {
		fmt.Fprintf(&b, "* Top-level: %s\n", strings.Join(i.TopLevelEntries, ", "))
	}
	if i.TrackedFileCount > 0 {
		fmt.Fprintf(&b, "* Tracked files: %d\n", i.TrackedFileCount)
	}
	if i.Branch != "" {
		fmt.Fprintf(&b, "* Branch: %s\n", i.Branch)
	}
	if i.HeadHash != "" {
		short := i.HeadHash
		if len(short) > 7 {
			short = short[:7]
		}
		fmt.Fprintf(&b, "* Last commit: %s (%s) — %s\n", short, i.HeadDate, i.HeadSubject)
	}
	if i.WorkingTreeStatus != "" {
		fmt.Fprintf(&b, "* Working tree: %s\n", i.WorkingTreeStatus)
		for _, f := range i.UncommittedFiles {
			fmt.Fprintf(&b, "    %s\n", f)
		}
	}
	if len(i.RecentCommits) > 0 {
		b.WriteString("* Recent commits (newest first):\n")
		for _, c := range i.RecentCommits {
			fmt.Fprintf(&b, "    %s  %s\n", c.ShortSHA, c.Subject)
		}
	}
	if len(i.DocsAtRoot) > 0 {
		fmt.Fprintf(&b, "* Docs at root: %s\n", strings.Join(i.DocsAtRoot, ", "))
	}
	if len(i.Manifests) > 0 {
		fmt.Fprintf(&b, "* Manifests detected: %s\n", strings.Join(i.Manifests, ", "))
	}

	b.WriteString("\nThe orientation above is current and authoritative. Do NOT re-run `ls` / `find` / `wc` / `git log` / `grep` on the project root or inside `.ateam/`. Don't look for files in `.ateam/` unless they are specifically listed elsewhere in this prompt — anything you find on your own may be stale, may belong to a different role, or may be a partial artifact from an unrelated run. Acting on it will produce incorrect or duplicated findings. The complete prior context for this role is whatever this prompt explicitly includes; there is nothing else to fetch.\n")
	return b.String()
}

// JSON renders Info as pretty-printed JSON.
func (i *Info) JSON() (string, error) {
	out, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return "", err
	}
	return string(out), nil
}
