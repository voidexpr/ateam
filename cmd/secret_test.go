package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
	"github.com/ateam/internal/secret"
)

func setupSecretFixture(t *testing.T) (projectDir, orgDir string, resolver *secret.Resolver) {
	t.Helper()
	base := t.TempDir()
	var err error
	orgDir, err = root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
	if err := os.MkdirAll(projPath, 0755); err != nil {
		t.Fatal(err)
	}
	projectDir, err = root.InitProject(projPath, orgDir, root.InitProjectOpts{
		Name:         "myproj",
		EnabledRoles: []string{"testing_basic"},
	})
	if err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	resolver = &secret.Resolver{
		Scopes: []secret.Scope{
			{Name: secret.ScopeProject, EnvFile: filepath.Join(projectDir, "secrets.env")},
			{Name: secret.ScopeOrg, EnvFile: filepath.Join(orgDir, "secrets.env")},
		},
		Backend: secret.BackendFile,
	}
	return
}

func TestListSecretsEmpty(t *testing.T) {
	projectDir, orgDir, resolver := setupSecretFixture(t)

	out := captureStdout(t, func() {
		if err := listSecrets(resolver, secret.BackendFile, projectDir, orgDir); err != nil {
			t.Fatalf("listSecrets: %v", err)
		}
	})

	// With default config, there may be no required secrets. Either we see
	// "No required secrets" or a list of secrets marked "not set".
	if !strings.Contains(out, "Storage:") {
		t.Errorf("expected 'Storage:' header in output:\n%s", out)
	}
}

func TestListSecretsWithValues(t *testing.T) {
	projectDir, orgDir, resolver := setupSecretFixture(t)

	// Seed a secret into the project scope.
	store := &secret.FileStore{Path: filepath.Join(projectDir, "secrets.env")}
	if err := store.Set("ANTHROPIC_API_KEY", "sk-test-123456"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	out := captureStdout(t, func() {
		if err := listSecrets(resolver, secret.BackendFile, projectDir, orgDir); err != nil {
			t.Fatalf("listSecrets: %v", err)
		}
	})

	if !strings.Contains(out, "Storage:") {
		t.Errorf("expected 'Storage:' header in output:\n%s", out)
	}
}

func TestGetSecretHappyPath(t *testing.T) {
	projectDir, _, resolver := setupSecretFixture(t)

	store := &secret.FileStore{Path: filepath.Join(projectDir, "secrets.env")}
	if err := store.Set("MY_KEY", "my-value"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	out := captureStdout(t, func() {
		if err := getSecret(resolver, "MY_KEY"); err != nil {
			t.Fatalf("getSecret: %v", err)
		}
	})

	if out != "my-value" {
		t.Errorf("getSecret output = %q, want %q", out, "my-value")
	}
}

func TestGetSecretMissing(t *testing.T) {
	_, _, resolver := setupSecretFixture(t)

	err := getSecret(resolver, "DOES_NOT_EXIST")
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !strings.Contains(err.Error(), "not set") {
		t.Errorf("expected 'not set' error, got: %v", err)
	}
}

func TestGetSecretEmptyStore(t *testing.T) {
	// Resolver with no scopes at all.
	resolver := &secret.Resolver{
		Scopes:  []secret.Scope{},
		Backend: secret.BackendFile,
	}

	err := getSecret(resolver, "ANY_KEY")
	if err == nil {
		t.Fatal("expected error for empty resolver")
	}
	if !strings.Contains(err.Error(), "not set") {
		t.Errorf("expected 'not set' error, got: %v", err)
	}
}

func TestGetSecretFromOrgScope(t *testing.T) {
	_, orgDir, resolver := setupSecretFixture(t)

	// Set only in org scope.
	store := &secret.FileStore{Path: filepath.Join(orgDir, "secrets.env")}
	if err := store.Set("ORG_KEY", "org-val"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	out := captureStdout(t, func() {
		if err := getSecret(resolver, "ORG_KEY"); err != nil {
			t.Fatalf("getSecret: %v", err)
		}
	})

	if out != "org-val" {
		t.Errorf("getSecret output = %q, want %q", out, "org-val")
	}
}

func TestGetSecretProjectOverridesOrg(t *testing.T) {
	projectDir, orgDir, resolver := setupSecretFixture(t)

	projectStore := &secret.FileStore{Path: filepath.Join(projectDir, "secrets.env")}
	orgStore := &secret.FileStore{Path: filepath.Join(orgDir, "secrets.env")}
	_ = projectStore.Set("SHARED", "project-val")
	_ = orgStore.Set("SHARED", "org-val")

	out := captureStdout(t, func() {
		if err := getSecret(resolver, "SHARED"); err != nil {
			t.Fatalf("getSecret: %v", err)
		}
	})

	if out != "project-val" {
		t.Errorf("expected project scope to win, got %q", out)
	}
}

func TestListSecretsFileStoreEmpty(t *testing.T) {
	dir := t.TempDir()
	store := &secret.FileStore{Path: filepath.Join(dir, "secrets.env")}

	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestListSecretsFileStoreNonexistent(t *testing.T) {
	store := &secret.FileStore{Path: filepath.Join(t.TempDir(), "no", "such.env")}

	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty list for nonexistent file, got %v", names)
	}
}

func TestListSecretsFileStoreMultipleKeys(t *testing.T) {
	dir := t.TempDir()
	store := &secret.FileStore{Path: filepath.Join(dir, "secrets.env")}

	_ = store.Set("KEY_A", "val-a")
	_ = store.Set("KEY_B", "val-b")
	_ = store.Set("KEY_C", "val-c")

	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(names), names)
	}

	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for _, want := range []string{"KEY_A", "KEY_B", "KEY_C"} {
		if !got[want] {
			t.Errorf("expected %q in list, got %v", want, names)
		}
	}
}
