package secret

import (
	"os"
	"path/filepath"
)

// Scope name constants.
const (
	ScopeGlobal  = "global"
	ScopeOrg     = "org"
	ScopeProject = "project"
)

// Scope represents a secret storage scope.
type Scope struct {
	Name        string // ScopeGlobal, ScopeOrg, or ScopeProject
	EnvFile     string // path to secrets.env
	KeychainKey string // stable ID for keychain account prefix (empty for global)
}

// Resolver walks the secret storage chain to find a value.
type Resolver struct {
	Scopes  []Scope // ordered: project, org, global (narrowest first)
	Backend Backend
}

// ResolveResult describes where a secret was found.
type ResolveResult struct {
	Value   string
	Source  string // "env", "project", "org", "global"
	Backend string // "env", "file", "keychain"
	Found   bool
}

// ResolverOpts holds optional configuration for building a Resolver.
type ResolverOpts struct {
	ProjectKeychainKey string // stable ID from config.toml for project-scoped keychain lookups
}

// NewResolver builds a Resolver from project/org directories.
// Scopes are ordered narrowest-first: project → org → global.
func NewResolver(projectDir, orgDir string, backend Backend, opts *ResolverOpts) *Resolver {
	var scopes []Scope

	if projectDir != "" {
		s := Scope{
			Name:    ScopeProject,
			EnvFile: filepath.Join(projectDir, "secrets.env"),
		}
		if opts != nil {
			s.KeychainKey = opts.ProjectKeychainKey
		}
		scopes = append(scopes, s)
	}
	if orgDir != "" {
		scopes = append(scopes, Scope{
			Name:    ScopeOrg,
			EnvFile: filepath.Join(orgDir, "secrets.env"),
		})
	}

	globalDir := GlobalDir()
	if globalDir != "" {
		scopes = append(scopes, Scope{
			Name:    ScopeGlobal,
			EnvFile: filepath.Join(globalDir, "secrets.env"),
		})
	}

	return &Resolver{Scopes: scopes, Backend: backend}
}

// Resolve looks up a secret through: scoped stores → env.
// The secret store is authoritative: if a value is configured via ateam secret,
// it always wins over inherited environment variables.
func (r *Resolver) Resolve(name string) ResolveResult {
	// Walk scopes first (project → org → global). Secret store is authoritative.
	for _, scope := range r.Scopes {
		if val, src, ok := r.resolveScope(scope, name); ok {
			return ResolveResult{Value: val, Source: scope.Name, Backend: src, Found: ok}
		}
	}

	// Fall back to process environment.
	if val, ok := os.LookupEnv(name); ok {
		return ResolveResult{Value: val, Source: "env", Backend: "env", Found: true}
	}

	return ResolveResult{}
}

// ResolveAll returns every location where `name` has a value, in priority
// order (project → org → global → env). Within a scope, both keychain and
// file are checked independently — so duplicates are surfaced. Unlike
// Resolve, it does not stop at the first match and ignores r.Backend
// preference (callers want to see every configured source).
func (r *Resolver) ResolveAll(name string) []ResolveResult {
	var results []ResolveResult
	for _, scope := range r.Scopes {
		if val, ok := lookupKeychain(scope, name); ok {
			results = append(results, ResolveResult{Value: val, Source: scope.Name, Backend: "keychain", Found: true})
		}
		if val, ok := lookupFile(scope, name); ok {
			results = append(results, ResolveResult{Value: val, Source: scope.Name, Backend: "file", Found: true})
		}
	}
	if val, ok := os.LookupEnv(name); ok {
		results = append(results, ResolveResult{Value: val, Source: "env", Backend: "env", Found: true})
	}
	return results
}

// resolveScope checks backends for a scope in priority order.
// Keychain is skipped entirely when keyringAvailable() is false.
func (r *Resolver) resolveScope(scope Scope, name string) (string, string, bool) {
	if r.Backend == BackendKeychain {
		if val, ok := lookupKeychain(scope, name); ok {
			return val, "keychain", true
		}
	}
	if val, ok := lookupFile(scope, name); ok {
		return val, "file", true
	}
	if r.Backend == BackendFile {
		if val, ok := lookupKeychain(scope, name); ok {
			return val, "keychain", true
		}
	}
	return "", "", false
}

// lookupKeychain returns the keychain value for (scope, name), or ok=false
// if the keychain is unavailable or the entry is missing/empty.
func lookupKeychain(scope Scope, name string) (string, bool) {
	if !keyringAvailable() {
		return "", false
	}
	val, err := KeychainGet(KeychainAccount(scope.Name, scope.KeychainKey, name))
	if err != nil || val == "" {
		return "", false
	}
	return val, true
}

// lookupFile returns the secrets.env value for (scope, name), or ok=false
// if the file is absent or the entry isn't set.
func lookupFile(scope Scope, name string) (string, bool) {
	store := &FileStore{Path: scope.EnvFile}
	val, ok, err := store.Get(name)
	if err != nil || !ok {
		return "", false
	}
	return val, true
}

// ScopeForName returns the scope matching the given name, or the default (last) scope.
func (r *Resolver) ScopeForName(name string) Scope {
	for _, s := range r.Scopes {
		if s.Name == name {
			return s
		}
	}
	if len(r.Scopes) > 0 {
		return r.Scopes[len(r.Scopes)-1]
	}
	return Scope{Name: ScopeGlobal, EnvFile: filepath.Join(GlobalDir(), "secrets.env")}
}
