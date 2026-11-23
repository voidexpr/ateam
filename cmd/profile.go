package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ateam/internal/runtime"
	"github.com/ateam/internal/sandboxpolicy"
	"github.com/spf13/cobra"
)

var (
	profileShowFlags   launchFlags
	profileSaveFlags   launchFlags
	profileExportFlags launchFlags
	profileSaveProject bool
	profileSaveForce   bool
	profileExportOut   string
)

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "List, inspect, save and export profiles",
}

var profileListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the profiles of every config layer",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, _, layers, err := launchPlaces()
		if err != nil {
			return err
		}
		cfg, err := runtime.Load(layers)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(cfg.Profiles))
		for name := range cfg.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			p := cfg.Profiles[name]
			var parts []string
			if p.Agent != "" {
				parts = append(parts, "agent="+p.Agent)
			}
			if p.Container != "" {
				parts = append(parts, "container="+p.Container)
			}
			if len(p.Use) > 0 {
				parts = append(parts, "use="+strings.Join(p.Use, ","))
			}
			if p.HasPolicy() {
				parts = append(parts, fmt.Sprintf("rules=%d", len(p.Read)+len(p.Write)+len(p.Exclude)+len(p.Domains)))
			}
			fmt.Printf("%-24s %s\n", name, strings.Join(parts, " "))
		}
		return nil
	},
}

var profileShowCmd = &cobra.Command{
	Use:   "show [NAME|FILE]...",
	Short: "Show a composed profile and where each value comes from",
	Long: `Show what a launch with the same profiles and flags would use: every
value with the profile it comes from, then the rules as the sandbox backend
gets them.

Example:
  ateam profile show sandbox/nono dev/go
  ateam profile show myproject --write ~/tmp/scratch`,
	RunE: func(cmd *cobra.Command, args []string) error {
		profileShowFlags.profiles = append(args, profileShowFlags.profiles...)
		_, _, layers, err := launchPlaces()
		if err != nil {
			return err
		}
		cfg, composed, _, err := composeLaunch(&profileShowFlags, layers)
		if err != nil {
			return err
		}
		prof, _, _, err := cfg.ResolveComposed(composed)
		if err != nil {
			return err
		}
		printComposedProfile(os.Stdout, prof)
		// The backend view needs a command; `true` stands in for one.
		plan, err := planLaunch(&profileShowFlags, modeShell, nil)
		if err != nil {
			fmt.Printf("\nnot launchable: %v\n", err)
			return nil
		}
		fmt.Println()
		plan.describe(os.Stdout)
		return nil
	},
}

func printComposedProfile(w io.Writer, p *runtime.ProfileConfig) {
	fmt.Fprintf(w, "profile %s\n", p.Name)
	scalar := func(attr, value string) {
		if value != "" {
			fmt.Fprintf(w, "  %-10s %s  (%s)\n", attr, value, p.Origin(attr))
		}
	}
	if len(p.Use) > 1 {
		fmt.Fprintf(w, "  %-10s %s\n", "composes", strings.Join(p.Use, ", "))
	}
	scalar("agent", p.Agent)
	scalar("container", p.Container)
	scalar("model", p.Model)
	scalar("effort", p.Effort)
	scalar("max_budget_usd", p.MaxBudgetUSD)
	scalar("ateam_files", p.AteamFiles)
	if len(p.Command) > 0 {
		fmt.Fprintf(w, "  %-10s %s  (%s)\n", "command", shellJoin(p.Command), p.Origin("command"))
	}
	list := func(attr string, entries []string) {
		for _, entry := range entries {
			fmt.Fprintf(w, "  %-10s %s  (%s)\n", attr, entry, p.EntryOrigin(attr, entry))
		}
	}
	list("write", p.Write)
	list("read", p.Read)
	list("exclude", p.Exclude)
	list("domains", p.Domains)
}

var profileSaveCmd = &cobra.Command{
	Use:   "save NAME",
	Short: "Save a profile built from flags",
	Long: `Write a profile that uses the profiles given with --profile and adds the
paths, domains and command given. Nothing is launched.

The file goes to the user's ateam directory (profiles/NAME.hcl), which a
sandboxed command cannot reach, or with --in-project to the project's
.ateam/profiles/.

Example:
  ateam profile save myproject --profile sandbox/nono,dev/go --write ~/SyncDatabox/dotfiles
  ateam chat --profile myproject`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return saveProfile(args[0], &profileSaveFlags)
	},
}

func hclList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// profileHCL renders one profile block from launch flags.
func profileHCL(name string, f *launchFlags) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Saved by `ateam profile save`. Edit freely: see `ateam profile show %s`.\n", name)
	fmt.Fprintf(&b, "profile %s {\n", strconv.Quote(name))
	attr := func(key string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "  %-7s = %s\n", key, hclList(items))
		}
	}
	attr("use", f.profiles)
	attr("write", f.write)
	attr("read", f.read)
	attr("exclude", f.exclude)
	attr("domains", f.domains)
	attr("command", strings.Fields(f.command))
	b.WriteString("}\n")
	return b.String()
}

func saveProfile(name string, f *launchFlags) error {
	if name == "" || strings.ContainsAny(name, " \t\n\"") {
		return fmt.Errorf("profile name %q: no spaces or quotes", name)
	}
	if _, files, err := splitProfileArgs(f.profiles); err != nil {
		return err
	} else if len(files) > 0 {
		return fmt.Errorf("a saved profile uses profiles by name, not by file: %s", files[0])
	}
	_, projectDir, layers, err := launchPlaces()
	if err != nil {
		return err
	}
	dir := filepath.Join(homeDirFlag(), runtime.ProfilesSubdir)
	if profileSaveProject {
		if projectDir == "" {
			return fmt.Errorf("--in-project: no project here")
		}
		dir = filepath.Join(projectDir, runtime.ProfilesSubdir)
	}
	dest := filepath.Join(dir, strings.ReplaceAll(name, "/", "_")+".hcl")
	if _, err := os.Stat(dest); err == nil && !profileSaveForce {
		return fmt.Errorf("%s exists; --force replaces it", dest)
	}
	// Check the profile in a scratch file before it lands where every
	// later launch loads it.
	tmp := dest + ".part"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, []byte(profileHCL(name, f)), 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	cfg, err := runtime.LoadWithFiles(layers, []string{tmp})
	if err != nil {
		return fmt.Errorf("the profile does not load: %w", err)
	}
	if !slices.Contains(cfg.ProfilesDefinedIn(tmp), name) {
		return fmt.Errorf("the profile %q did not load from %s", name, tmp)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	fmt.Printf("saved %s\n", dest)
	return nil
}

var profileExportCmd = &cobra.Command{
	Use:   "export [NAME|FILE]... --out DIR",
	Short: "Write a profile as its sandbox backend's own config, usable without ateam",
	Long: `Write the backend profile of a launch, a script with the command line and
a list of every rule with its origin. The files reference nothing of
ateam: edit them, or run ateam itself under them.

Example:
  ateam profile export myproject --out ~/sandboxes/myproject
  ~/sandboxes/myproject/run.sh make test
  ateam profile export myproject --backend fence --out ~/sandboxes/myproject-fence`,
	RunE: func(cmd *cobra.Command, args []string) error {
		profileExportFlags.profiles = append(args, profileExportFlags.profiles...)
		return exportProfile(&profileExportFlags, profileExportOut)
	},
}

func exportProfile(f *launchFlags, out string) error {
	if out == "" {
		return fmt.Errorf("--out DIR is required")
	}
	out, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	plan, err := planLaunch(f, modeShell, nil)
	if err != nil {
		return err
	}
	if plan.policy == nil {
		return fmt.Errorf("profile %q has no sandbox backend to export (container %q)", plan.prof.Name, plan.container.Name)
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		return err
	}
	fence := plan.container.Type == runtime.ContainerTypeFence
	profileName := "profile.json"
	if fence {
		profileName = "fence.json"
	}
	profileFile := filepath.Join(out, profileName)
	plan.argv = []string{"true"}
	argv, err := plan.wrapped(context.Background(), modeRun, profileFile)
	if err != nil {
		return err
	}
	prefix := argv[:len(argv)-1] // without the stand-in command
	run := "exec " + shellJoin(prefix)
	if fence {
		// Without ateam the file is run by the fence command; the prefix
		// printed below runs it with the fence built into ateam.
		run = "cd " + shellJoin([]string{plan.policy.WorkDir}) + " && exec " + shellJoin(sandboxpolicy.FenceCLIPrefix(profileFile))
	}
	script := "#!/bin/sh\n# Runs a command under " + profileFile + ", without ateam.\n# Usage: run.sh COMMAND [ARG]...\n" + run + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(out, "run.sh"), []byte(script), 0o700); err != nil {
		return err
	}
	var rules strings.Builder
	fmt.Fprintf(&rules, "# Rules of profile %s\n\nGenerated by `ateam profile export`. Rules marked `ateam` protect ateam's own files; remove or add entries in %s by hand.\n\n```\n", plan.prof.Name, profileName)
	plan.describe(&rules)
	rules.WriteString("```\n")
	if err := os.WriteFile(filepath.Join(out, "RULES.md"), []byte(rules.String()), 0o600); err != nil {
		return err
	}
	fmt.Printf("wrote %s: %s, run.sh, RULES.md\n\n%s COMMAND [ARG]...\n", out, profileName, shellJoin(prefix))
	return nil
}

func init() {
	addLaunchFlags(profileShowCmd, &profileShowFlags)
	addLaunchFlags(profileSaveCmd, &profileSaveFlags)
	profileSaveCmd.Flags().StringVar(&profileSaveFlags.command, "command", "", "command the profile saves for `ateam run` (split on spaces)")
	profileSaveCmd.Flags().BoolVar(&profileSaveProject, "in-project", false, "save into the project's .ateam/profiles/ instead of the user's")
	profileSaveCmd.Flags().BoolVar(&profileSaveForce, "force", false, "replace an existing file")
	addLaunchFlags(profileExportCmd, &profileExportFlags)
	profileExportCmd.Flags().StringVar(&profileExportOut, "out", "", "directory to write into")
	profileExportCmd.Flags().StringVar(&profileExportFlags.backend, "backend", "", "sandbox backend to export for, instead of the profile's: nono or fence")
	profileCmd.AddCommand(profileListCmd, profileShowCmd, profileSaveCmd, profileExportCmd)
	rootCmd.AddCommand(profileCmd)
}
