package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/prompts/assembler"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
	"github.com/spf13/cobra"
)

var (
	promptRole                 string
	promptAction               string
	promptExtraPrompt          string
	promptPrePrompt            string
	promptPostPrompt           string
	promptNoProjectInfo        bool
	promptIgnorePreviousReport bool
	promptSupervisor           bool
	promptPaths                bool
	promptInlinePaths          bool
)

var promptCmd = &cobra.Command{
	Use:   "prompt",
	Short: "Resolve and print the full prompt for a role or supervisor",
	Long: `Perform 3-level prompt resolution (project → org → defaults) for a given
role or supervisor action, then print the assembled prompt to stdout.

Example:
  ateam prompt --role security --action report
  ateam prompt --role refactor_small --action code
  ateam prompt --role security --action report --extra-prompt "Focus on auth"
  ateam prompt --supervisor --action review
  ateam prompt --supervisor --action code
  ateam prompt --supervisor --action verify
  ateam prompt --role security --action report --paths
  ateam prompt --role security --action report --inline-paths`,
	Args: cobra.NoArgs,
	RunE: runPrompt,
}

func init() {
	promptCmd.Flags().StringVar(&promptRole, "role", "", "role name")
	promptCmd.Flags().BoolVar(&promptSupervisor, "supervisor", false, "generate supervisor prompt instead of role prompt")
	promptCmd.Flags().StringVar(&promptAction, "action", "", "action type: report, code, review, or verify (required)")
	addPromptWrapFlags(promptCmd, &promptExtraPrompt, &promptPrePrompt, &promptPostPrompt)
	promptCmd.Flags().BoolVar(&promptNoProjectInfo, "no-project-info", false, "omit ateam project context from the prompt")
	promptCmd.Flags().BoolVar(&promptIgnorePreviousReport, "ignore-previous-report", false, "do not include the role's previous report in the prompt")
	promptCmd.Flags().BoolVar(&promptPaths, "paths", false, "show a per-section breakdown table (slot + anchor + path + mod time + tokens); no prompt body")
	promptCmd.Flags().BoolVar(&promptInlinePaths, "inline-paths", false, "print the full prompt with each section preceded by an anchor/path/mod-time/tokens header; troubleshooting view, not for agent consumption")
	promptCmd.MarkFlagsMutuallyExclusive("role", "supervisor")
	promptCmd.MarkFlagsMutuallyExclusive("paths", "inline-paths")
	_ = promptCmd.MarkFlagRequired("action")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	if promptInlinePaths {
		return runPromptInlinePaths()
	}
	if promptPaths {
		return runPromptPaths()
	}
	if promptSupervisor {
		return runPromptSupervisor()
	}
	if promptRole == "" {
		return fmt.Errorf("either --role or --supervisor is required")
	}
	return runPromptRole()
}

func runPromptRole() error {
	if promptAction != runner.ActionReport && promptAction != runner.ActionCode {
		return fmt.Errorf("invalid action %q for role: must be 'report' or 'code'", promptAction)
	}

	env, err := resolveEnv()
	if err != nil {
		return err
	}

	if !prompts.IsValidRole(promptRole, env.Config.Roles, env.ProjectDir, env.OrgDir) {
		return fmt.Errorf("unknown role: %s\nValid roles: %s", promptRole, strings.Join(prompts.AllKnownRoleIDs(env.Config.Roles, env.ProjectDir, env.OrgDir), ", "))
	}

	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return err
	}
	prePrompt, err := prompts.ResolveOptional(promptPrePrompt)
	if err != nil {
		return err
	}
	postPrompt, err := prompts.ResolveOptional(promptPostPrompt)
	if err != nil {
		return err
	}

	roleLabel := "role " + promptRole
	if promptNoProjectInfo {
		roleLabel = ""
	}

	var assembled string
	switch promptAction {
	case runner.ActionReport:
		assembled, err = assembleRoleReport(env, promptRole, roleLabel, extraPrompt, prePrompt, postPrompt, promptIgnorePreviousReport)
	case runner.ActionCode:
		assembled, err = assembleRoleCode(env, promptRole, roleLabel, extraPrompt, prePrompt, postPrompt)
	}
	if err != nil {
		return err
	}
	fmt.Println(assembled)
	return nil
}

func runPromptSupervisor() error {
	if promptAction != runner.ActionReview && promptAction != runner.ActionCode && promptAction != runner.ActionVerify {
		return fmt.Errorf("invalid action %q for supervisor: must be 'review', 'code', or 'verify'", promptAction)
	}

	env, err := resolveEnv()
	if err != nil {
		return err
	}

	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return err
	}
	prePrompt, err := prompts.ResolveOptional(promptPrePrompt)
	if err != nil {
		return err
	}
	postPrompt, err := prompts.ResolveOptional(promptPostPrompt)
	if err != nil {
		return err
	}

	roleLabel := "the supervisor"
	if promptNoProjectInfo {
		roleLabel = ""
	}

	var assembled string
	switch promptAction {
	case runner.ActionReview:
		assembled, err = assembleReview(env, prompts.ReviewSelector{}, roleLabel, extraPrompt, "", prePrompt, postPrompt)
	case runner.ActionCode:
		reviewContent, readErr := os.ReadFile(env.ReviewPath())
		if readErr != nil {
			return errNoReview(env.ReviewPath())
		}
		// Use the same helper cmd/code.go invokes so the preview matches what
		// the real run sends — including the Sub-Run Flags block. Exact flag
		// values (batch, profile, model, ...) depend on the live `ateam code`
		// invocation, so the preview uses placeholders that show the shape;
		// the user sees "this becomes a real value at run time."
		assembled, err = assembleCodeManagementV1(env, roleLabel, string(reviewContent), previewSubRunFlags(env.SourceDir), extraPrompt, "", prePrompt, postPrompt)
	case runner.ActionVerify:
		assembled, err = assembleSupervisor(env, "code_verify", roleLabel, "verify", extraPrompt, prePrompt, postPrompt)
	}
	if err != nil {
		return err
	}
	fmt.Println(assembled)
	return nil
}

// sectionDigest is one row of per-section metadata both --paths and
// --inline-paths emit. Centralizes the path-prefix / mod-time / token-count
// computation so the two modes share one shape.
type sectionDigest struct {
	Slot     string
	Anchor   string
	Path     string // anchor-rooted, e.g. ".ateam/prompts/_pre.context.md"
	Modified string // human-readable, "embedded"/"<date>" or "<date> (build)" fallback
	Tokens   int
	Content  string // rendered, post-template-expansion
}

// assembleForInspection runs the v1 assembler for the current flag set and
// returns the assembled result plus per-section digests in one call. Both
// --paths and --inline-paths use it; the only difference between the two
// modes is how they format these digests for output.
//
// Honors the same flag suite that runPromptRole / runPromptSupervisor do —
// --no-project-info, --extra-prompt, --ignore-previous-report — so the
// inspection output reflects what the corresponding `ateam report` /
// `ateam review` / `ateam code` / `ateam verify` run would assemble.
//
// Sections beyond the assembler's composition (previous-report, reports
// manifest, review body, sub-run flags, --extra-prompt) are recorded with
// Anchor="live" and a descriptive Path so the user can tell them from
// fragment files on disk.
//
// Orphan fragments are surfaced to stderr and turned into a hard error per
// the spec's "errors loudly on orphan fragments".
func assembleForInspection() (string, []sectionDigest, error) {
	env, err := resolveEnv()
	if err != nil {
		return "", nil, err
	}
	extraPrompt, err := prompts.ResolveOptional(promptExtraPrompt)
	if err != nil {
		return "", nil, err
	}
	prePrompt, err := prompts.ResolveOptional(promptPrePrompt)
	if err != nil {
		return "", nil, err
	}
	postPrompt, err := prompts.ResolveOptional(promptPostPrompt)
	if err != nil {
		return "", nil, err
	}
	promptPath, defaultLabel, err := promptPathForCurrentFlags()
	if err != nil {
		return "", nil, err
	}
	roleLabel := defaultLabel
	if promptNoProjectInfo {
		roleLabel = ""
	}
	a := env.Assembler()

	// Orphan scan: every orphan is surfaced to stderr for visibility, but only
	// orphans tied to the previewed prompt block the preview. An orphan is
	// "tied" when it sits in the previewed directory AND its role either equals
	// the previewed role or is a near-miss typo of it (Hint). A stray fragment
	// for an unrelated role (e.g. a deleted role's leftover .post.*) must not
	// fail `ateam prompt --paths` when the real `ateam report` run — which
	// never calls FindOrphans — succeeds for the previewed role.
	previewDir, previewRole := splitPromptPath(promptPath)
	orphans, err := a.FindOrphans()
	if err != nil {
		return "", nil, fmt.Errorf("scanning for orphan fragments: %w", err)
	}
	var blocking int
	for _, o := range orphans {
		fmt.Fprintln(os.Stderr, o.Error())
		if o.Dir == previewDir && (o.Role == previewRole || o.Hint == previewRole) {
			blocking++
		}
	}
	if blocking > 0 {
		return "", nil, fmt.Errorf("found %d orphan fragment(s) for the previewed prompt %q; fix or remove them before assembling", blocking, promptPath)
	}

	vars := env.BuildAssemblerVars(promptPath, roleLabel, promptAction)
	// Pre-prompt rides through the assembler (lands as the front cli_pre_prompt
	// section); post-prompt is appended after the synthesized live sections
	// below so the preview mirrors the real run's outermost-tail ordering.
	res, err := a.Assemble(promptPath, vars, nil, &assembler.AssembleOptions{PrePrompt: prePrompt})
	if err != nil {
		return "", nil, err
	}

	anchorFS := anchorFSMap(a)
	digests := make([]sectionDigest, 0, len(res.Sections)+3)
	for _, s := range res.Sections {
		digests = append(digests, sectionDigest{
			Slot:     s.Slot,
			Anchor:   s.Anchor,
			Path:     displayAnchorPath(env, s.Anchor, s.Path),
			Modified: sectionModTime(anchorFS, s.Anchor, s.Path),
			Tokens:   prompts.EstimateTokens(s.Content),
			Content:  s.Content,
		})
	}

	addLive := func(slot, source, content string) {
		if content == "" {
			return
		}
		digests = append(digests, sectionDigest{
			Slot:     slot,
			Anchor:   "live",
			Path:     source,
			Modified: "-",
			Tokens:   prompts.EstimateTokens(content),
			Content:  content,
		})
	}
	addExtra := func() {
		if extraPrompt != "" {
			addLive("extra_prompt", "(--extra-prompt)", "# Additional Instructions\n\n"+extraPrompt)
		}
	}

	if promptSupervisor {
		switch promptAction {
		case runner.ActionReview:
			all, derr := prompts.DiscoverReports(env.ProjectDir)
			if derr == nil {
				reports, _ := (prompts.ReviewSelector{}).Filter(all, env.Config.Roles)
				addLive("reports", "(assembleReview: manifest + bundled role reports)", formatReportsBlock(reports))
			}
			addExtra()
		case runner.ActionCode:
			reviewContent, readErr := os.ReadFile(env.ReviewPath())
			if readErr != nil {
				return "", nil, errNoReview(env.ReviewPath())
			}
			addLive("review", env.ReviewPath(), "# Review\n\n"+string(reviewContent))
			// extra_prompt goes BEFORE sub_run_flags to match assembleCodeManagementV1's
			// ordering — the supervisor's last context is the flag list.
			addExtra()
			addLive("sub_run_flags", "(cmd/code.go: rendered from --batch / --profile / --agent / --model / --effort / --max-budget-*)", previewSubRunFlags(env.SourceDir).Render())
		case runner.ActionVerify:
			addExtra()
		}
	} else {
		switch promptAction {
		case runner.ActionReport:
			if !promptIgnorePreviousReport {
				addLive("previous_report", env.RoleReportPath(promptRole), previousReportBlock(env, promptRole))
			}
			addExtra()
		case runner.ActionCode:
			addExtra()
		}
	}

	// CLI post-prompt is the outermost tail wrapper, after every synthesized
	// live section — matching where the real run appends it.
	post, perr := renderCLIWrapper(a, vars, postPrompt)
	if perr != nil {
		return "", nil, perr
	}
	addLive("cli_post_prompt", "(--post-prompt)", post)

	return promptPath, digests, nil
}

// splitPromptPath splits a v1 prompt path ("report/security", "review") into
// its directory and trailing role/name segments — the same (dir, role) shape
// FindOrphans records for each fragment, so the inspection orphan check can
// match orphans against the previewed prompt.
func splitPromptPath(promptPath string) (dir, role string) {
	parts := strings.Split(promptPath, "/")
	role = parts[len(parts)-1]
	dir = strings.Join(parts[:len(parts)-1], "/")
	return dir, role
}

// runPromptPaths emits the per-section table that --paths produces:
// columns slot / anchor / path / mod-time / est-tokens plus a TOTAL row.
// No prompt body is printed — use --inline-paths if you want the rendered
// content alongside the metadata.
func runPromptPaths() error {
	promptPath, digests, err := assembleForInspection()
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Assembly for %q (%d sections)\n\n", promptPath, len(digests))
	fmt.Fprintln(w, "SLOT\tANCHOR\tPATH\tLAST MODIFIED\tEST. TOKENS")
	var totalTokens int
	for _, d := range digests {
		totalTokens += d.Tokens
		fmt.Fprintf(w, "%s\t[%s]\t%s\t%s\t%s\n",
			d.Slot, d.Anchor, d.Path, d.Modified, display.FmtTokens(int64(d.Tokens)))
	}
	fmt.Fprintf(w, "TOTAL\t\t\t\t%s\n", display.FmtTokens(int64(totalTokens)))
	w.Flush()
	return nil
}

// anchorFSMap indexes an Assembler's anchors by name so the preview can
// stat each section's source file via the FS that owns it (project's
// os.DirFS gives real mod times; the embedded anchor's fs.Sub gives a
// zero time, which sectionModTime renders as "embedded").
func anchorFSMap(a *assembler.Assembler) map[string]fs.FS {
	anchors := a.Anchors()
	out := make(map[string]fs.FS, len(anchors))
	for _, anc := range anchors {
		out[anc.Name] = anc.FS
	}
	return out
}

// sectionModTime stats path against the anchor's FS and returns a
// human-readable mod-time string. For project / org anchors that's the
// file's real ModTime. For the embedded anchor, embed.FS reports the
// zero time, so we substitute the binary's own build time — the content
// is frozen at build time, so the build time IS effectively the embedded
// file's last-modified. Stat failures surface as "-".
func sectionModTime(anchorFS map[string]fs.FS, anchor, path string) string {
	fsys, ok := anchorFS[anchor]
	if !ok {
		return "-"
	}
	info, err := fs.Stat(fsys, path)
	if err != nil {
		return "-"
	}
	t := info.ModTime()
	if t.IsZero() {
		if bt := ParseBuildTime(BuildTime); !bt.IsZero() {
			return display.FmtDateAge(bt) + " (build)"
		}
		return "embedded"
	}
	return display.FmtDateAge(t)
}

// runPromptInlinePaths prints the rendered prompt with each composed
// section preceded by a metadata header. Same data as --paths, just
// interleaved with the content so a human can visually trace any output
// paragraph back to its source file:
//
//	==================================================================
//	[embedded] defaults/prompts/_pre.context.md
//	slot: root_pre   modified: 05/28 (just now) (build)   tokens: 660
//	==================================================================
//
//	<rendered content of that file>
//
// The headers are not markdown, so the output is for a human only — feed
// the agent the body from a regular run instead.
func runPromptInlinePaths() error {
	_, digests, err := assembleForInspection()
	if err != nil {
		return err
	}
	const rule = "=================================================================="
	for i, d := range digests {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(rule)
		fmt.Printf("[%s] %s\n", d.Anchor, d.Path)
		fmt.Printf("slot: %s   modified: %s   tokens: %s\n",
			d.Slot, d.Modified, display.FmtTokens(int64(d.Tokens)))
		fmt.Println(rule)
		fmt.Println()
		fmt.Println(d.Content)
	}
	return nil
}

// displayAnchorPath formats an anchor-relative path for human-readable
// preview output. Paths from the project anchor are prefixed with
// `.ateam/prompts/`, org with `.ateamorg/prompts/`, embedded with
// `defaults/prompts/` (the in-source location of the built-in prompts).
// Unknown anchor names fall through to the bare path.
func displayAnchorPath(env *root.ResolvedEnv, anchor, relPath string) string {
	switch anchor {
	case "project":
		return ".ateam/prompts/" + relPath
	case "org":
		return ".ateamorg/prompts/" + relPath
	case "embedded":
		return "defaults/prompts/" + relPath
	}
	return relPath
}

// promptPathForCurrentFlags maps the existing --role/--supervisor/--action
// flag combo to the v1 promptPath (e.g. "report/security", "review") plus a
// roleLabel for the project info block. The mapping intentionally mirrors
// the old runPromptRole / runPromptSupervisor branches so preview output
// matches what `ateam report` etc. would actually assemble.
func promptPathForCurrentFlags() (path, label string, err error) {
	if promptSupervisor {
		switch promptAction {
		case runner.ActionReview:
			return "review", "the supervisor", nil
		case runner.ActionCode:
			return "code_management", "the supervisor", nil
		case runner.ActionVerify:
			return "code_verify", "the supervisor", nil
		}
		return "", "", fmt.Errorf("invalid action %q for supervisor: must be 'review', 'code', or 'verify'", promptAction)
	}
	if promptRole == "" {
		return "", "", fmt.Errorf("either --role or --supervisor is required")
	}
	switch promptAction {
	case runner.ActionReport:
		return "report/" + promptRole, "role " + promptRole, nil
	case runner.ActionCode:
		return "code/" + promptRole, "role " + promptRole, nil
	}
	return "", "", fmt.Errorf("invalid action %q for role: must be 'report' or 'code'", promptAction)
}
