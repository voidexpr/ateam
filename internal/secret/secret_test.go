package secret

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ateam/internal/runtime"
)

// --- FileStore tests ---

func TestFileStoreSetAndGet(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	if err := store.Set("API_KEY", "secret123"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, ok, err := store.Get("API_KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected key to be found")
	}
	if val != "secret123" {
		t.Fatalf("expected 'secret123', got %q", val)
	}
}

func TestFileStoreGetMissing(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	_, ok, err := store.Get("NONEXISTENT")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("expected key not found")
	}
}

func TestFileStoreGetFromNonexistentFile(t *testing.T) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), "no", "such", "file.env")}
	_, ok, err := store.Get("KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("expected key not found for nonexistent file")
	}
}

func TestFileStoreSetOverwrite(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	_ = store.Set("KEY", "v1")
	_ = store.Set("KEY", "v2")

	val, ok, err := store.Get("KEY")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || val != "v2" {
		t.Fatalf("expected 'v2', got %q (found=%v)", val, ok)
	}
}

func TestFileStoreSetCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "dir")
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	if err := store.Set("KEY", "val"); err != nil {
		t.Fatalf("Set should create parent dirs: %v", err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("expected directory to be created")
	}
}

func TestFileStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	_ = store.Set("A", "1")
	_ = store.Set("B", "2")

	found, err := store.Delete("A")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found for deletion")
	}

	_, ok, err2 := store.Get("A")
	if err2 != nil {
		t.Fatalf("Get: %v", err2)
	}
	if ok {
		t.Fatal("expected key to be gone after deletion")
	}

	val, ok, err2 := store.Get("B")
	if err2 != nil {
		t.Fatalf("Get: %v", err2)
	}
	if !ok || val != "2" {
		t.Fatal("expected other key to remain")
	}
}

func TestFileStoreDeleteMissing(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	_ = store.Set("A", "1")

	found, err := store.Delete("NONEXISTENT")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if found {
		t.Fatal("expected key not found")
	}
}

func TestFileStoreDeleteFromNonexistentFile(t *testing.T) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), "nope.env")}
	found, err := store.Delete("KEY")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if found {
		t.Fatal("expected key not found for nonexistent file")
	}
}

func TestFileStoreList(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}

	_ = store.Set("X", "1")
	_ = store.Set("Y", "2")

	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(names))
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	if !got["X"] || !got["Y"] {
		t.Fatalf("expected X and Y, got %v", names)
	}
}

// --- parseLine tests ---

func TestParseLine(t *testing.T) {
	tests := []struct {
		input string
		key   string
		val   string
	}{
		{"API_KEY=secret", "API_KEY", "secret"},
		{"KEY=value with spaces", "KEY", "value with spaces"},
		{"KEY=val=ue", "KEY", "val=ue"},
		{"  KEY = value", "KEY", " value"},
		{"", "", ""},
		{"# comment", "", ""},
		{"  # indented comment", "", ""},
		{"NOEQUALSSIGN", "", ""},
		{"EMPTY=", "EMPTY", ""},
	}

	for _, tt := range tests {
		k, v := parseLine(tt.input)
		if k != tt.key || v != tt.val {
			t.Errorf("parseLine(%q) = (%q, %q), want (%q, %q)", tt.input, k, v, tt.key, tt.val)
		}
	}
}

// --- Resolver tests ---

func TestResolverScopePrecedence(t *testing.T) {
	projectDir := t.TempDir()
	orgDir := t.TempDir()

	projectStore := &FileStore{Path: filepath.Join(projectDir, "secrets.env")}
	orgStore := &FileStore{Path: filepath.Join(orgDir, "secrets.env")}

	_ = projectStore.Set("SHARED_KEY", "project-value")
	_ = orgStore.Set("SHARED_KEY", "org-value")
	_ = orgStore.Set("ORG_ONLY", "org-only-value")

	r := &Resolver{
		Scopes: []Scope{
			{Name: ScopeProject, EnvFile: filepath.Join(projectDir, "secrets.env")},
			{Name: ScopeOrg, EnvFile: filepath.Join(orgDir, "secrets.env")},
		},
		Backend: BackendFile,
	}

	result := r.Resolve("SHARED_KEY")
	if !result.Found {
		t.Fatal("expected SHARED_KEY to be found")
	}
	if result.Value != "project-value" {
		t.Fatalf("expected project scope to win, got %q from %q", result.Value, result.Source)
	}
	if result.Source != ScopeProject {
		t.Fatalf("expected source 'project', got %q", result.Source)
	}

	result = r.Resolve("ORG_ONLY")
	if !result.Found {
		t.Fatal("expected ORG_ONLY to be found")
	}
	if result.Value != "org-only-value" {
		t.Fatalf("expected 'org-only-value', got %q", result.Value)
	}
	if result.Source != ScopeOrg {
		t.Fatalf("expected source 'org', got %q", result.Source)
	}
}

func TestResolverEnvOverridesScopes(t *testing.T) {
	projectDir := t.TempDir()
	projectStore := &FileStore{Path: filepath.Join(projectDir, "secrets.env")}
	_ = projectStore.Set("TEST_SECRET_ENV_VAR", "file-value")

	t.Setenv("TEST_SECRET_ENV_VAR", "env-value")

	r := &Resolver{
		Scopes: []Scope{
			{Name: ScopeProject, EnvFile: filepath.Join(projectDir, "secrets.env")},
		},
		Backend: BackendFile,
	}

	result := r.Resolve("TEST_SECRET_ENV_VAR")
	if !result.Found {
		t.Fatal("expected key to be found")
	}
	if result.Value != "env-value" {
		t.Fatalf("expected env to win, got %q from %q", result.Value, result.Source)
	}
	if result.Source != "env" {
		t.Fatalf("expected source 'env', got %q", result.Source)
	}
}

func TestResolverNotFound(t *testing.T) {
	r := &Resolver{
		Scopes:  []Scope{},
		Backend: BackendFile,
	}
	result := r.Resolve("DOES_NOT_EXIST_ANYWHERE")
	if result.Found {
		t.Fatal("expected key not found")
	}
}

// --- ScopeForName tests ---

func TestScopeForName(t *testing.T) {
	r := &Resolver{
		Scopes: []Scope{
			{Name: ScopeProject, EnvFile: "/p/secrets.env"},
			{Name: ScopeOrg, EnvFile: "/o/secrets.env"},
			{Name: ScopeGlobal, EnvFile: "/g/secrets.env"},
		},
		Backend: BackendFile,
	}

	s := r.ScopeForName(ScopeOrg)
	if s.Name != ScopeOrg {
		t.Fatalf("expected org scope, got %q", s.Name)
	}

	s = r.ScopeForName(ScopeProject)
	if s.Name != ScopeProject {
		t.Fatalf("expected project scope, got %q", s.Name)
	}

	s = r.ScopeForName(ScopeGlobal)
	if s.Name != ScopeGlobal {
		t.Fatalf("expected global scope, got %q", s.Name)
	}
}

func TestScopeForNameFallback(t *testing.T) {
	r := &Resolver{
		Scopes: []Scope{
			{Name: ScopeProject, EnvFile: "/p/secrets.env"},
			{Name: ScopeOrg, EnvFile: "/o/secrets.env"},
		},
		Backend: BackendFile,
	}

	s := r.ScopeForName("nonexistent")
	if s.Name != ScopeOrg {
		t.Fatalf("expected fallback to last scope (org), got %q", s.Name)
	}
}

func TestScopeForNameEmpty(t *testing.T) {
	r := &Resolver{Scopes: []Scope{}, Backend: BackendFile}
	s := r.ScopeForName("anything")
	if s.Name != ScopeGlobal {
		t.Fatalf("expected fallback to global, got %q", s.Name)
	}
}

// --- ValidateSecrets tests ---

func TestValidateSecretsAllPresent(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}
	_ = store.Set("MY_API_KEY", "key123")

	r := &Resolver{
		Scopes: []Scope{
			{Name: ScopeProject, EnvFile: filepath.Join(dir, "secrets.env")},
		},
		Backend: BackendFile,
	}

	ac := &runtime.AgentConfig{
		RequiredEnv: []string{"MY_API_KEY"},
	}

	if err := ValidateSecrets(ac, r); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateSecretsMissing(t *testing.T) {
	r := &Resolver{
		Scopes:  []Scope{},
		Backend: BackendFile,
	}

	ac := &runtime.AgentConfig{
		RequiredEnv: []string{"TOTALLY_MISSING_KEY"},
	}

	err := ValidateSecrets(ac, r)
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func TestValidateSecretsAlternatives(t *testing.T) {
	dir := t.TempDir()
	store := &FileStore{Path: filepath.Join(dir, "secrets.env")}
	_ = store.Set("ALT_B", "val")

	r := &Resolver{
		Scopes: []Scope{
			{Name: ScopeProject, EnvFile: filepath.Join(dir, "secrets.env")},
		},
		Backend: BackendFile,
	}

	ac := &runtime.AgentConfig{
		RequiredEnv: []string{"ALT_A|ALT_B"},
	}

	if err := ValidateSecrets(ac, r); err != nil {
		t.Fatalf("expected no error when alternative is present, got: %v", err)
	}
}

func TestValidateSecretsNilResolver(t *testing.T) {
	ac := &runtime.AgentConfig{
		RequiredEnv: []string{"KEY"},
	}
	if err := ValidateSecrets(ac, nil); err != nil {
		t.Fatalf("expected nil resolver to return nil error, got: %v", err)
	}
}

func TestValidateSecretsNoRequiredEnv(t *testing.T) {
	r := &Resolver{Scopes: []Scope{}, Backend: BackendFile}
	ac := &runtime.AgentConfig{}
	if err := ValidateSecrets(ac, r); err != nil {
		t.Fatalf("expected no error with no required env, got: %v", err)
	}
}

// --- CollectRequiredEnvNames tests ---

func TestCollectRequiredEnvNames(t *testing.T) {
	cfg := &runtime.Config{
		Agents: map[string]runtime.AgentConfig{
			"a1": {RequiredEnv: []string{"KEY_A", "KEY_B|KEY_C"}},
			"a2": {RequiredEnv: []string{"KEY_A", "KEY_D"}},
		},
		Containers: map[string]runtime.ContainerConfig{
			"c1": {ForwardEnv: []string{"KEY_E", "KEY_A"}},
		},
	}

	names := CollectRequiredEnvNames(cfg)
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}

	for _, expected := range []string{"KEY_A", "KEY_B", "KEY_C", "KEY_D", "KEY_E"} {
		if !got[expected] {
			t.Errorf("expected %q in collected names, got %v", expected, names)
		}
	}
	if len(names) != 5 {
		t.Errorf("expected 5 unique names, got %d: %v", len(names), names)
	}
}

func TestCollectRequiredEnvNamesEmpty(t *testing.T) {
	cfg := &runtime.Config{
		Agents:     map[string]runtime.AgentConfig{},
		Containers: map[string]runtime.ContainerConfig{},
	}
	names := CollectRequiredEnvNames(cfg)
	if len(names) != 0 {
		t.Fatalf("expected no names, got %v", names)
	}
}
