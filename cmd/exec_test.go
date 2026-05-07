package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runner"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = pw
	fn()
	pw.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, pr)
	return buf.String()
}

func TestPrintExecDryRun(t *testing.T) {
	r := &runner.Runner{
		Agent:   &agent.MockAgent{},
		Profile: "test",
	}
	env := &root.ResolvedEnv{}

	out := captureStdout(t, func() {
		if err := printExecDryRun(r, env, "hello world", "security", ""); err != nil {
			t.Errorf("printExecDryRun: %v", err)
		}
	})

	for _, want := range []string{"mock", "dry-run", "Profile:", "hello world"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}
}

func TestRunExecDryRunNoExec(t *testing.T) {
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	savedOrg, savedDryRun, savedQuiet, savedAgent, savedProfile, savedRole :=
		orgFlag, execDryRun, execQuiet, execAgent, execProfile, execRole
	defer func() {
		orgFlag, execDryRun, execQuiet, execAgent, execProfile, execRole =
			savedOrg, savedDryRun, savedQuiet, savedAgent, savedProfile, savedRole
	}()
	orgFlag = filepath.Dir(orgDir) // --org takes the parent of .ateamorg/
	execDryRun = true
	execQuiet = true
	execAgent = "mock"
	execProfile = ""
	execRole = ""

	var runErr error
	captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runExec(nil, []string{"test prompt"})
		})
	})

	if runErr != nil {
		t.Fatalf("runExec dry-run: %v", runErr)
	}
}
