package root

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ateam/internal/config"
	"github.com/ateam/internal/runtime"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// RegistryFile is the project registry under <HomeDir>/state: one
// `project "<name>" { path = "..." }` block per project `ateam init`
// created. It maps the project's identity (its name) to its location, for
// `ateam projects`, `ateam serve` and the duplicate-name check.
const RegistryFile = "projects.hcl"

// RegistryEntry is one registered project.
type RegistryEntry struct {
	Name        string
	Path        string // absolute path of the project root (parent of .ateam/)
	KeychainKey string // stable id for project-scoped keychain secrets
}

type hclRegistry struct {
	Projects []struct {
		Name        string `hcl:"name,label"`
		Path        string `hcl:"path"`
		KeychainKey string `hcl:"keychain_key,optional"`
	} `hcl:"project,block"`
}

func registryPath(homeDir string) string {
	return filepath.Join(homeDir, runtime.StateSubdir, RegistryFile)
}

// LoadRegistry reads the registry; a missing file is an empty registry.
func LoadRegistry(homeDir string) ([]RegistryEntry, error) {
	path := registryPath(homeDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	file, diags := hclparse.NewParser().ParseHCL(data, path)
	if diags.HasErrors() {
		return nil, fmt.Errorf("cannot parse %s: %w", path, diags)
	}
	var reg hclRegistry
	if diags := gohcl.DecodeBody(file.Body, nil, &reg); diags.HasErrors() {
		return nil, fmt.Errorf("cannot parse %s: %w", path, diags)
	}
	entries := make([]RegistryEntry, 0, len(reg.Projects))
	for _, p := range reg.Projects {
		entries = append(entries, RegistryEntry{Name: p.Name, Path: p.Path, KeychainKey: p.KeychainKey})
	}
	return entries, nil
}

// SaveRegistry writes the registry, sorted by name.
func SaveRegistry(homeDir string, entries []RegistryEntry) error {
	path := registryPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("cannot create %s: %w", filepath.Dir(path), err)
	}
	sorted := append([]RegistryEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	b.WriteString("# Projects created with `ateam init`, by name. Maintained by ateam;\n")
	b.WriteString("# `ateam project-rename` fixes an entry after a directory move.\n\n")
	for _, e := range sorted {
		fmt.Fprintf(&b, "project %s {\n  path = %s\n", strconv.Quote(e.Name), strconv.Quote(e.Path))
		if e.KeychainKey != "" {
			fmt.Fprintf(&b, "  keychain_key = %s\n", strconv.Quote(e.KeychainKey))
		}
		b.WriteString("}\n\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// LookupRegistry returns the entry registered under name.
func LookupRegistry(homeDir, name string) (RegistryEntry, bool) {
	entries, err := LoadRegistry(homeDir)
	if err != nil {
		return RegistryEntry{}, false
	}
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return RegistryEntry{}, false
}

// Register adds or replaces the entry for e.Name.
func Register(homeDir string, e RegistryEntry) error {
	entries, err := LoadRegistry(homeDir)
	if err != nil {
		return err
	}
	return SaveRegistry(homeDir, append(without(entries, e.Name), e))
}

// Unregister removes the entry for name; a missing entry is not an error.
func Unregister(homeDir, name string) error {
	entries, err := LoadRegistry(homeDir)
	if err != nil {
		return err
	}
	return SaveRegistry(homeDir, without(entries, name))
}

func without(entries []RegistryEntry, name string) []RegistryEntry {
	kept := entries[:0]
	for _, x := range entries {
		if x.Name != name {
			kept = append(kept, x)
		}
	}
	return kept
}

// ProjectInfo holds a registered project whose .ateam/ still exists.
type ProjectInfo struct {
	Name   string
	Dir    string   // the project's .ateam/
	Layers []string // config layers, lowest precedence first
	Config *config.Config
}

// WalkProjects enumerates registered projects in name order, skipping
// entries whose directory or configuration no longer loads. The callback
// receives each project. Return filepath.SkipAll to stop early.
func WalkProjects(homeDir string, fn func(ProjectInfo) error) error {
	entries, err := LoadRegistry(homeDir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, e := range entries {
		projDir := filepath.Join(e.Path, DirName)
		if runtime.ProjectName(projDir) == "" {
			continue
		}
		env, err := resolveFrom(homeDir, projDir)
		if err != nil || env.ProjectDir == "" {
			continue
		}
		if err := fn(ProjectInfo{Name: env.ProjectName, Dir: env.ProjectDir, Layers: env.Layers, Config: env.Config}); err != nil {
			if err == filepath.SkipAll {
				return nil
			}
			return err
		}
	}
	return nil
}
