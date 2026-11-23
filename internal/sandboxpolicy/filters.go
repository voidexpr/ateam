package sandboxpolicy

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/voidexpr/autoversion/filterrules"
)

// FilterSet holds named filters in the shared filter syntax (the one of
// autoversion and targy): name -> text. A profile's read, write and
// exclude lists name them beside literal paths.
type FilterSet map[string]string

// Lookup implements filterrules.Resolver: a filter may `use` another.
func (fs FilterSet) Lookup(name string) (*filterrules.File, error) {
	text, ok := fs[name]
	if !ok {
		return nil, nil
	}
	return filterrules.ParseFileWith(strings.NewReader(text), fs)
}

// Names implements filterrules.Resolver.
func (fs FilterSet) Names() []string {
	names := make([]string, 0, len(fs))
	for name := range fs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Paths resolves a filter into the absolute paths it includes and
// excludes, the filters it uses included. Its rules apply under its root
// line, or the home without one. A sandbox takes paths, not patterns: a
// rule with a wildcard, a type prefix or a marker is an error.
func (fs FilterSet) Paths(name, home string) (include, exclude []string, err error) {
	text, ok := fs[name]
	if !ok {
		return nil, nil, fmt.Errorf("unknown filter %q (known: %s)", name, strings.Join(fs.Names(), ", "))
	}
	fail := func(err error) ([]string, []string, error) {
		return nil, nil, fmt.Errorf("filter %q: %w", name, err)
	}
	file, err := filterrules.ParseFileWith(strings.NewReader(text), fs)
	if err != nil {
		return fail(err)
	}
	root := home
	if file.Root != "" {
		if root, err = ExpandPath(file.Root, home); err != nil {
			return fail(fmt.Errorf("root %s: %w", file.Root, err))
		}
	}
	rules, err := filterrules.NewWith(file, fs)
	if err != nil {
		return fail(err)
	}
	for _, rule := range rules.List() {
		if !rule.Literal() || rule.Type != filterrules.TypeAny {
			return fail(fmt.Errorf("%q is not one path: a sandbox rule names a path, without wildcard or type", rule.String()))
		}
		path := filepath.Join(root, rule.Text)
		if rule.Kind == filterrules.Include {
			include = append(include, path)
		} else {
			exclude = append(exclude, path)
		}
	}
	return include, exclude, nil
}

// Check resolves every filter, so a mistake shows when the configuration
// loads and not at the first launch that uses the filter.
func (fs FilterSet) Check() error {
	for _, name := range fs.Names() {
		if _, _, err := fs.Paths(name, "/home/check"); err != nil {
			return err
		}
	}
	return nil
}

// isPathEntry reports whether a profile entry is a path, not a filter name.
func isPathEntry(entry string) bool {
	return entry == "~" || strings.HasPrefix(entry, "~/") || filepath.IsAbs(entry)
}
