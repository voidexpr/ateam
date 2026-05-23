package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

// savedContainerCpGlobals captures the package-level flags + resolver hook
// runContainerCp consults so tests can mutate them and restore.
type savedContainerCpGlobals struct {
	profile   string
	container string
	dryRun    bool
	resolver  func(string) (string, error)
	orgFlag   string
}

func saveContainerCpGlobals() savedContainerCpGlobals {
	return savedContainerCpGlobals{
		profile:   containerCpProfile,
		container: containerCpContainer,
		dryRun:    containerCpDryRun,
		resolver:  resolveContainerName,
		orgFlag:   orgFlag,
	}
}

func (s savedContainerCpGlobals) restore() {
	containerCpProfile = s.profile
	containerCpContainer = s.container
	containerCpDryRun = s.dryRun
	resolveContainerName = s.resolver
	orgFlag = s.orgFlag
}

// setupContainerCpProject creates an org + project fixture with a runtime.hcl
// profile that resolves to the given Docker container name. Returns the
// project root path and the .ateamorg/ directory.
func setupContainerCpProject(t *testing.T, profileName, dockerContainer string) (projPath, orgDir string) {
	t.Helper()
	base := t.TempDir()
	var err error
	orgDir, err = root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath = filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	initTestGitRepo(t, projPath)
	if _, err := root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	}); err != nil {
		t.Fatalf("InitProject: %v", err)
	}
	hcl := fmt.Sprintf(`
container "myct" {
  type             = "docker"
  docker_container = %q
}

profile %q {
  agent     = "claude"
  container = "myct"
}
`, dockerContainer, profileName)
	if err := os.WriteFile(filepath.Join(projPath, ".ateam", "runtime.hcl"), []byte(hcl), 0644); err != nil {
		t.Fatal(err)
	}
	return projPath, orgDir
}

// TestContainerCPDryRunPrintsPlan exercises the happy-path dry-run: a profile
// resolves to a Docker container name and the plan output names both the
// container and the in-container binary destination without firing the
// copy. We stub resolveContainerName so the test never shells out to docker
// (which would also need a matching running container).
func TestContainerCPDryRunPrintsPlan(t *testing.T) {
	projPath, orgDir := setupContainerCpProject(t, "p", "foo")

	// findLinuxBinary searches a fixed set of paths. On non-linux/amd64
	// hosts none of them exist under `go test` (the test exe lives in a
	// temp dir with no go.mod for cross-compile fallback), so seed the
	// orgDir cache slot with a placeholder so the dry-run can resolve it.
	cacheDir := filepath.Join(orgDir, "cache")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, linuxCompanionName()), []byte("stub"), 0755); err != nil {
		t.Fatalf("seed linux binary stub: %v", err)
	}

	saved := saveContainerCpGlobals()
	defer saved.restore()

	containerCpProfile = "p"
	containerCpContainer = ""
	containerCpDryRun = true
	orgFlag = ""

	var resolverCalls int
	var resolverArg string
	resolveContainerName = func(name string) (string, error) {
		resolverCalls++
		resolverArg = name
		return name, nil
	}

	var runErr error
	out := captureStdout(t, func() {
		withChdir(t, projPath, func() {
			runErr = runContainerCp(nil, nil)
		})
	})

	if runErr != nil {
		t.Fatalf("runContainerCp: %v", runErr)
	}
	for _, want := range []string{"[dry-run]", "foo", ateamContainerBinPath} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in dry-run output:\n%s", want, out)
		}
	}
	if resolverCalls != 1 {
		t.Errorf("resolveContainerName called %d times, want 1", resolverCalls)
	}
	if resolverArg != "foo" {
		t.Errorf("resolveContainerName received %q, want %q", resolverArg, "foo")
	}
}

// TestContainerCPNoFlagsFails locks in the "must specify a container source"
// validation. With neither --container-name nor --profile we expect a clear
// error before any Docker work is attempted.
func TestContainerCPNoFlagsFails(t *testing.T) {
	saved := saveContainerCpGlobals()
	defer saved.restore()

	containerCpProfile = ""
	containerCpContainer = ""
	containerCpDryRun = true

	var resolverCalls int
	resolveContainerName = func(name string) (string, error) {
		resolverCalls++
		return name, nil
	}

	err := runContainerCp(nil, nil)
	if err == nil {
		t.Fatal("expected error for no --container-name or --profile, got nil")
	}
	if !strings.Contains(err.Error(), "--container-name") || !strings.Contains(err.Error(), "--profile") {
		t.Errorf("error message should mention both flags: %v", err)
	}
	if resolverCalls != 0 {
		t.Errorf("resolveContainerName must not run on validation failure (got %d calls)", resolverCalls)
	}
}

// TestContainerCPProfileMissingDockerContainerFails ensures that picking a
// profile whose container has no docker_container value surfaces a targeted
// error rather than later resolving "" against Docker.
func TestContainerCPProfileMissingDockerContainerFails(t *testing.T) {
	projPath, _ := setupContainerCpProject(t, "p", "")

	saved := saveContainerCpGlobals()
	defer saved.restore()

	containerCpProfile = "p"
	containerCpContainer = ""
	containerCpDryRun = true
	orgFlag = ""

	var resolverCalls int
	resolveContainerName = func(name string) (string, error) {
		resolverCalls++
		return name, nil
	}

	var runErr error
	withChdir(t, projPath, func() {
		runErr = runContainerCp(nil, nil)
	})

	if runErr == nil {
		t.Fatal("expected error for profile without docker_container, got nil")
	}
	if !strings.Contains(runErr.Error(), "docker_container") {
		t.Errorf("error should mention docker_container: %v", runErr)
	}
	if resolverCalls != 0 {
		t.Errorf("resolveContainerName must not run when profile lacks docker_container (got %d calls)", resolverCalls)
	}
}

// TestContainerCPMissingBinaryFailsClearly drives the dry-run past container
// resolution to the binary lookup, then asserts the actionable error when no
// Linux ateam binary is present. On linux hosts findLinuxBinary always
// returns os.Executable(), so this path is only reachable elsewhere.
func TestContainerCPMissingBinaryFailsClearly(t *testing.T) {
	if goruntime.GOOS == "linux" {
		t.Skip("linux host always finds the running binary; missing-binary path unreachable")
	}

	projPath, _ := setupContainerCpProject(t, "p", "foo")

	saved := saveContainerCpGlobals()
	defer saved.restore()

	containerCpProfile = "p"
	containerCpContainer = ""
	containerCpDryRun = true
	orgFlag = ""

	resolveContainerName = func(name string) (string, error) {
		return name, nil
	}

	var runErr error
	withChdir(t, projPath, func() {
		runErr = runContainerCp(nil, nil)
	})

	if runErr == nil {
		t.Fatal("expected missing-binary error, got nil")
	}
	if !strings.Contains(runErr.Error(), "no linux ateam binary") {
		t.Errorf("error should mention missing linux binary: %v", runErr)
	}
}
