package sandboxprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/ateam/defaults"
)

// ProbesDir holds the probe scripts in the embedded defaults.
const ProbesDir = "probes"

const scriptTimeout = 60 * time.Second

// scratchPrefix starts the name of the folder a script runs in.
const scratchPrefix = "ateam-probe-scratch-"

// Script is an active check: a shell script that exercises a capability.
// Its header is described in DEV.md, "Adding a probe".
type Script struct {
	ID     string
	Group  string
	Target string
	Why    string
	Starts string
	Risk   string   // the result that is a risk: works, fails or none
	Needs  []string // programs; without one the result is absent
	Body   string
}

// Scripts returns the embedded probe scripts, sorted by id.
func Scripts() ([]Script, error) {
	return LoadScripts(defaults.FS, ProbesDir)
}

// LoadScripts parses every *.sh file of dir in fsys.
func LoadScripts(fsys fs.FS, dir string) ([]Script, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*.sh"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var scripts []Script
	for _, file := range files {
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, err
		}
		script, err := ParseScript(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if want := strings.TrimSuffix(path.Base(file), ".sh"); script.ID != want {
			return nil, fmt.Errorf("%s: id is %q, want the file's name %q", file, script.ID, want)
		}
		scripts = append(scripts, script)
	}
	return scripts, nil
}

// ParseScript reads the header of a probe script: the `# key: value` lines
// before its first command. Every field but needs is required.
func ParseScript(text string) (Script, error) {
	script := Script{Body: text}
	fields := map[string]*string{"id": &script.ID, "group": &script.Group, "target": &script.Target,
		"why": &script.Why, "starts": &script.Starts, "risk": &script.Risk}
	for _, line := range strings.Split(text, "\n") {
		comment, ok := strings.CutPrefix(line, "#")
		if !ok {
			if strings.TrimSpace(line) == "" {
				continue
			}
			break
		}
		key, value, ok := strings.Cut(comment, ":")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || strings.HasPrefix(comment, "!") || strings.Contains(key, " ") {
			continue
		}
		switch field := fields[key]; {
		case key == "needs":
			script.Needs = strings.Fields(value)
		case field == nil:
			return script, fmt.Errorf("unknown header field %q", key)
		default:
			*field = value
		}
	}
	for _, key := range []string{"id", "group", "target", "why", "starts", "risk"} {
		if *fields[key] == "" {
			return script, fmt.Errorf("header field %q is missing", key)
		}
	}
	if script.Risk != "works" && script.Risk != "fails" && script.Risk != "none" {
		return script, fmt.Errorf("risk is %q: want works, fails or none", script.Risk)
	}
	return script, nil
}

// Check returns the check that runs the script: off unless asked for.
func (s Script) Check() Check {
	check := Check{ID: s.ID, Group: s.Group, Type: TypeCapability, Method: MethodScript, Target: s.ID,
		Name: s.Target, Why: s.Why, Active: true, Starts: s.Starts}
	switch s.Risk {
	case "works":
		check.Safe = safe(Fails, Absent)
	case "fails":
		check.Safe = safe(Works, Absent)
	}
	return check
}

// Run runs the script with sh, in a scratch folder that is removed
// after. Exit 0 is works, 1 is fails; any other end is fails without a
// judgment (forced ok). The first line it prints is the detail.
func (s Script) Run(env Env) (perm Permission, detail string, forced Verdict) {
	for _, program := range s.Needs {
		if env.lookPath(program) == "" {
			return Absent, program + " is not in PATH", ""
		}
	}
	scratch, err := os.MkdirTemp("", scratchPrefix)
	if err != nil {
		return Fails, "unknown: " + err.Error(), OK
	}
	defer os.RemoveAll(scratch)
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-s")
	cmd.Dir = scratch
	cmd.Stdin = strings.NewReader(s.Body)
	cmd.Env = append(os.Environ(), "ATEAM_PROBE_WORKDIR="+env.WorkDir, "ATEAM_PROBE_HOME="+env.Home)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.WaitDelay = 5 * time.Second
	err = cmd.Run()
	detail, _, _ = strings.Cut(strings.TrimSpace(out.String()), "\n")
	var exit *exec.ExitError
	switch {
	case err == nil:
		return Works, detail, ""
	case ctx.Err() != nil:
		return Fails, joinDetail("unknown: no answer in "+scriptTimeout.String(), detail), OK
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return Fails, detail, ""
	case errors.As(err, &exit):
		return Fails, joinDetail(fmt.Sprintf("unknown: exit %d", exit.ExitCode()), detail), OK
	}
	return Fails, "unknown: " + err.Error(), OK
}
