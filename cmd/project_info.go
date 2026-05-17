package cmd

import (
	"fmt"

	"github.com/ateam/internal/projectinfo"
	"github.com/spf13/cobra"
)

var (
	projectInfoFormat string
)

var projectInfoCmd = &cobra.Command{
	Use:   "project-info [path]",
	Short: "Emit generic, language-agnostic orientation about a project",
	Long: `Collect a small set of generic facts about a git repository:
top-level entries, tracked-file count, recent commits, docs at root,
detected manifest files, and HEAD / working-tree status.

The data is language- and build-system-agnostic — it depends only on
git. This is the "Universal core" layer described in
plans/Feature_TokenReduction.md (Phase 0.5). The output is exposed as
a Go API (internal/projectinfo) and rendered as markdown or JSON, but
is NOT yet wired into role prompts; that's a follow-up step.

Examples:
  ateam project-info
  ateam project-info /path/to/repo
  ateam project-info --format json
  ateam project-info --format json | jq .head_hash`,
	RunE: runProjectInfo,
}

func init() {
	projectInfoCmd.Flags().StringVar(&projectInfoFormat, "format", "markdown", "output format: markdown | json")
}

func runProjectInfo(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	info, err := projectinfo.Collect(dir)
	if err != nil {
		return err
	}
	switch projectInfoFormat {
	case "markdown", "md":
		fmt.Print(info.Markdown())
	case "json":
		s, err := info.JSON()
		if err != nil {
			return err
		}
		fmt.Println(s)
	default:
		return fmt.Errorf("unknown format %q (want: markdown, json)", projectInfoFormat)
	}
	return nil
}
