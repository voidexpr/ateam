package promptdata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/gitutil"
)

// SandboxSettingsFile is the standalone sandbox-settings JSON shipped
// alongside the embedded prompts (see DefaultSandboxSettings).
const SandboxSettingsFile = "ateam_claude_sandbox_extra_settings.json"

// AutoRolesMarker is the contract line the `--auto-roles` planner agent
// writes at the end of its output file. The substring after the colon is
// comma-separated role IDs (no spaces); an empty value means "no roles need
// running". Substituted into the prompt as `{{auto_roles_marker}}` and parsed
// back by cmd/auto_roles.go::parseAutoRolesOutput. Keeping it as a single
// exported constant prevents prompt-vs-parser drift.
const AutoRolesMarker = "RECOMMENDED_ROLES:"

// ProjectInfoParams holds the values needed to build the project info section.
type ProjectInfoParams struct {
	OrgDir      string // absolute path to .ateamorg/
	ProjectDir  string // absolute path to .ateam/
	ProjectName string
	WorkDir     string // absolute path of the agent's working directory
	GitRepoDir  string // absolute path to git repo root (may differ from WorkDir)
	Role        string // e.g. "role security" or "the supervisor"
	Action      string // e.g. "report", "review", "code"
	Meta        *gitutil.ProjectMeta

	// QuickOrientation is appended to the project context as an auto-generated
	// orientation block (top-level layout, recent commits, detected manifests,
	// …). Produced by internal/projectinfo.Info.Markdown(). Empty when
	// collection fails. See plans/Feature_TokenReduction.md (Phase 0.5).
	QuickOrientation string
}

// FormatProjectInfo builds the ateam project context section.
// Returns "" if p has no Role set (zero value).
func FormatProjectInfo(p ProjectInfoParams) string {
	if p.Role == "" {
		return ""
	}
	var b strings.Builder
	if p.ProjectName != "" {
		b.WriteString(p.ProjectName)
		b.WriteString(" ")
		b.WriteString(p.Role)
		if p.Action != "" {
			b.WriteString(" ")
			b.WriteString(p.Action)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("# ATeam Project Context\n\n")
	b.WriteString("You are part of the ateam software:\n")
	fmt.Fprintf(&b, "* project name: %s\n", p.ProjectName)
	fmt.Fprintf(&b, "* role: %s\n", p.Role)
	b.WriteString("* working directory: .\n")
	ateamRel := ".ateam"
	if p.WorkDir != "" && p.ProjectDir != "" {
		ateamRel = shortRelPath(p.WorkDir, p.ProjectDir)
	}
	fmt.Fprintf(&b, "* reports and reviews: %s\n", ateamRel)
	if p.GitRepoDir != "" && p.GitRepoDir != p.WorkDir {
		rel := shortRelPath(p.WorkDir, p.GitRepoDir)
		fmt.Fprintf(&b, "\n**IMPORTANT**: Your working directory (.) is a subdirectory of a wider git repo at %s. Limit your findings to the working directory. Do not look at or report on code outside it.\n", rel)
	}
	if p.Meta != nil {
		ts := time.Now().Format(display.TimestampFormat)
		fmt.Fprintf(&b, "* timestamp: %s\n", ts)
		if p.QuickOrientation == "" {
			hash := p.Meta.CommitHash
			if len(hash) > 12 {
				hash = hash[:12]
			}
			fmt.Fprintf(&b, "* last commit: %s - %s - \"%s\"\n", hash, p.Meta.CommitDate, p.Meta.CommitMessage)
			if len(p.Meta.Uncommitted) > 0 {
				fmt.Fprintf(&b, "* uncommitted changes: %d file(s)\n", len(p.Meta.Uncommitted))
				for _, f := range p.Meta.Uncommitted {
					fmt.Fprintf(&b, "  * `%s`\n", f)
				}
			} else {
				b.WriteString("* working tree: clean\n")
			}
		}
	}
	if p.QuickOrientation != "" {
		b.WriteString("\n")
		b.WriteString(p.QuickOrientation)
	}
	return b.String()
}

// shortRelPath returns target relative to base. Falls back to ~/... shorthand
// for paths under $HOME, or the absolute path if neither works.
func shortRelPath(base, target string) string {
	if rel, err := filepath.Rel(base, target); err == nil && !filepath.IsAbs(rel) {
		return rel
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, target); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return target
}

// WriteIfNotExists writes content to path only if the file does not already exist.
func WriteIfNotExists(path, content string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(content)
	return err
}
