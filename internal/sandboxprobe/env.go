package sandboxprobe

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/ateam/internal/sandboxpolicy"
)

// Env is what the built-in checks are made from. Tests build one for a
// scratch home.
type Env struct {
	GOOS       string
	Home       string
	WorkDir    string
	Shell      string
	Path       []string
	Executable string // the ateam binary
	TmpDir     string
	AteamHome  string
	ProjectDir string // the project's .ateam directory; "" for <WorkDir>/.ateam
	// Secrets are the paths nothing should read: the embedded agent/secrets
	// filter, resolved by the caller.
	Secrets []string
	// Names are the names of the environment variables, without values.
	Names  []string
	Getenv func(string) string
}

// CurrentEnv describes the process's own environment.
func CurrentEnv(ateamHome string, secrets []string) (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	work, err := os.Getwd()
	if err != nil {
		return Env{}, err
	}
	exe, _ := os.Executable()
	env := Env{
		GOOS: goruntime.GOOS, Home: sandboxpolicy.RealPath(home), WorkDir: sandboxpolicy.RealPath(work),
		Shell: os.Getenv("SHELL"), Path: filepath.SplitList(os.Getenv("PATH")),
		Executable: exe, TmpDir: os.Getenv("TMPDIR"), AteamHome: ateamHome,
		Secrets: secrets, Getenv: os.Getenv,
	}
	for _, kv := range os.Environ() {
		if name, value, ok := strings.Cut(kv, "="); ok && value != "" {
			env.Names = append(env.Names, name)
		}
	}
	return env, nil
}

// name is how the report shows a path: relative to the working folder,
// else with ~ for the home. It is also what makes an id the same on two
// machines.
func (e Env) name(path string) string {
	switch {
	case path == e.WorkDir:
		return "./"
	case sandboxpolicy.Within(path, e.WorkDir):
		return path[len(e.WorkDir)+1:]
	case path == e.Home:
		return "~"
	case sandboxpolicy.Within(path, e.Home):
		return "~" + path[len(e.Home):]
	}
	return path
}

func (e Env) file(group, path, why string, safe []Permission) Check {
	name := e.name(path)
	return Check{ID: group + ":" + name, Group: group, Type: TypeFile, Method: MethodFile, Target: path, Name: name, Why: why, Safe: safe}
}

func (e Env) files(group, why string, safe []Permission, paths ...string) []Check {
	checks := make([]Check, 0, len(paths))
	for _, path := range paths {
		checks = append(checks, e.file(group, path, why, safe))
	}
	return checks
}

func (e Env) home(parts ...string) string {
	return filepath.Join(append([]string{e.Home}, parts...)...)
}

func (e Env) work(parts ...string) string {
	return filepath.Join(append([]string{e.WorkDir}, parts...)...)
}

// lookPath finds a program in the Env's PATH: the first regular,
// executable file of that name. A name with a separator is taken as is.
func (e Env) lookPath(name string) string {
	if strings.ContainsRune(name, filepath.Separator) {
		if isExecutable(name) {
			return name
		}
		return ""
	}
	for _, dir := range e.Path {
		if dir == "" {
			continue
		}
		if path := filepath.Join(dir, name); isExecutable(path) {
			return path
		}
	}
	return ""
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}
