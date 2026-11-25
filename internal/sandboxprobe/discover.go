package sandboxprobe

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ateam/internal/sandboxpolicy"
)

// Discovery is bounded: a rule lists at most maxListed entries and
// produces at most maxRows checks.
const (
	maxListed = 2000
	maxRows   = 200
)

// secretNames are the file names, lower-case, that suggest a secret.
var secretNames = []string{"id_*", "*.pem", "*.key", "*.p12", "*.pfx", ".env*", "*credentials*", "*token*", ".netrc"}

// plainSuffixes end names that match a pattern without holding a secret:
// public keys, and source or text files in a working folder.
var plainSuffixes = []string{
	".pub", ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".java", ".c", ".h", ".cc", ".cpp", ".rb",
	".md", ".txt", ".html", ".css", ".sh", ".example", ".sample", ".template", ".dist",
}

// secretVars are the parts of a variable name that suggest a secret, and
// knownVars the names that hold one without such a part.
var (
	secretVars = []string{"TOKEN", "KEY", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL"}
	knownVars  = []string{"DATABASE_URL", "KUBECONFIG", "NETRC"}
)

// plainVars are patterns of names that match secretVars and hold no
// secret. Extend it as cases show up.
var plainVars = []string{
	"KEYBOARD*", "*KEYMAP*", "XKB_*", "KEYTIMEOUT", "LESSKEY*", "SSH_ASKPASS", "GPG_TTY", "*KEYRING*",
	"KEYCHAIN_*", "TOKENIZERS_*", "PASSWORD_STORE_DIR", "*_KEY_BINDINGS", "*KEYBIND*", "MONKEY*", "*_KEYWORDS",
	"GIT_CONFIG_KEY_*",
}

// Discover looks for what the fixed lists do not name, by name and by
// place: files that look like secrets, variables that look like tokens,
// the projects next to the working folder, and on Linux the mounts. No
// file content is read. known are the paths the fixed lists already check.
func Discover(env Env, known []string) []Check {
	covered := func(file string) bool {
		real := sandboxpolicy.RealPath(file)
		return slices.ContainsFunc(known, func(k string) bool { return sandboxpolicy.Within(real, k) || sandboxpolicy.Within(file, k) })
	}
	var checks []Check
	names := slices.Concat(
		findNames(env.Home, 1, nil),
		findNames(env.home(".config"), 2, nil),
		findNames(env.WorkDir, 3, []string{".git", "node_modules", "vendor"}),
	)
	names = capRows("secret names", names)
	for _, file := range names {
		if !covered(file) {
			check := env.file("secrets", file, whySecret+": found by its name", closed)
			known = append(known, file)
			checks = append(checks, check)
		}
	}
	for _, name := range capRows("environment variables", secretVarNames(env.Names)) {
		checks = append(checks, Check{ID: "secrets:$" + name, Group: "secrets", Type: TypeEnv, Method: MethodEnv,
			Target: name, Name: name, Why: "a variable that looks like a credential: every command started here gets it", Safe: safe(Absent)})
	}
	for _, dir := range capRows("sibling projects", siblingProjects(env.WorkDir)) {
		if dir != env.Home {
			checks = append(checks, env.file("where", dir, "another project can be changed", readOnly))
		}
	}
	return checks
}

// MountChecks returns one check per mount shared from outside, from a
// mountinfo text. A writable home or root, and a Docker socket, are risks.
func MountChecks(env Env, mountinfo string) []Check {
	var checks []Check
	for _, mount := range ParseMounts(mountinfo) {
		check := env.file("mounts", mount.Point, "", nil)
		check.Method, check.Known = MethodKnown, Write
		if mount.ReadOnly {
			check.Known = Read
		}
		check.KnownDetail = mount.FSType
		if mount.Root != "/" && mount.Root != mount.Point {
			check.KnownDetail += " from " + mount.Root
		}
		switch base := filepath.Base(mount.Point); {
		case strings.HasSuffix(base, ".sock"):
			check.Safe, check.Why = closed, "a socket of the host"
		case mount.Point == env.Home || isHomePath(mount.Root):
			check.Safe, check.Why = readOnly, "a home of the host, writable"
		}
		checks = append(checks, check)
	}
	return capRows("mounts", checks)
}

// isHomePath reports whether path is a user's home on the host.
func isHomePath(p string) bool {
	if p == "/root" {
		return true
	}
	dir, name := path.Split(p)
	return name != "" && (dir == "/home/" || dir == "/Users/")
}

func capRows[T any](rule string, rows []T) []T {
	if len(rows) > maxRows {
		fmt.Fprintf(os.Stderr, "ateam probe: discovery of %s stopped at %d rows of %d\n", rule, maxRows, len(rows))
		return rows[:maxRows]
	}
	return rows
}

// findNames lists root to the given depth and returns the entries whose
// name looks like a secret. Folders named in skip are not entered.
func findNames(root string, depth int, skip []string) []string {
	var found []string
	listed := 0
	var walk func(dir string, left int)
	walk = func(dir string, left int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, entry := range entries {
			if listed++; listed > maxListed {
				if listed == maxListed+1 {
					fmt.Fprintf(os.Stderr, "ateam probe: discovery under %s stopped after %d entries\n", root, maxListed)
				}
				return
			}
			name := entry.Name()
			if looksSecret(name) {
				found = append(found, filepath.Join(dir, name))
				continue
			}
			if entry.IsDir() && left > 1 && !slices.Contains(skip, name) {
				walk(filepath.Join(dir, name), left-1)
			}
		}
	}
	walk(root, depth)
	return found
}

func looksSecret(name string) bool {
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, markerPrefix) || slices.ContainsFunc(plainSuffixes, func(s string) bool { return strings.HasSuffix(lower, s) }) {
		return false
	}
	return matchAny(secretNames, lower)
}

func matchAny(patterns []string, name string) bool {
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		ok, _ := path.Match(pattern, name)
		return ok
	})
}

// secretVarNames returns the variable names that look like a credential.
// It never sees a value.
func secretVarNames(names []string) []string {
	var found []string
	for _, name := range names {
		upper := strings.ToUpper(name)
		if matchAny(plainVars, upper) {
			continue
		}
		if slices.Contains(knownVars, upper) || slices.ContainsFunc(secretVars, func(part string) bool { return strings.Contains(upper, part) }) {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	return found
}

// siblingProjects returns the folders next to work that hold a .git.
func siblingProjects(work string) []string {
	parent := filepath.Dir(work)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var found []string
	for i, entry := range entries {
		if i >= maxListed {
			break
		}
		dir := filepath.Join(parent, entry.Name())
		if dir == work {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			found = append(found, dir)
		}
	}
	return found
}
