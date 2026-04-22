// Package secret provides secret storage and retrieval using multiple backends including the system keyring and file-based stores.
package secret

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// Backend identifies a secret storage backend.
type Backend string

const (
	BackendFile     Backend = "file"
	BackendKeychain Backend = "keychain"

	keychainService = "ateam"
)

// DefaultBackend returns the preferred backend for the current OS.
// Returns keychain if a keyring backend is available, otherwise file.
func DefaultBackend() Backend {
	if keyringAvailable() {
		return BackendKeychain
	}
	return BackendFile
}

var (
	keyringOnce  sync.Once
	keyringAvail bool
)

// keyringAvailable probes whether the OS keyring is functional.
// Result is cached for the process lifetime.
func keyringAvailable() bool {
	keyringOnce.Do(func() {
		_, err := keyring.Get(keychainService, "__probe__")
		keyringAvail = err == nil || err == keyring.ErrNotFound
	})
	return keyringAvail
}

// GlobalDir returns the global secrets directory (~/.config/ateam/).
func GlobalDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ateam")
}

// --- File backend (.env) ---

// FileStore reads/writes secrets from a plain KEY=VALUE file.
type FileStore struct {
	Path string
}

// Get returns the value for name, or ("", false, nil) if not found.
// Returns a non-nil error only for real I/O failures (not missing files).
func (s *FileStore) Get(name string) (string, bool, error) {
	entries, err := s.readAll()
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	val, ok := entries[name]
	return val, ok, nil
}

// Set writes or updates name=value in the file.
func (s *FileStore) Set(name, value string) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}

	lines, _ := readLines(s.Path)
	found := false
	for i, line := range lines {
		key, _ := parseLine(line)
		if key == name {
			lines[i] = name + "=" + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, name+"="+value)
	}
	return os.WriteFile(s.Path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

// Delete removes a key from the file. Returns false if the key was not found.
func (s *FileStore) Delete(name string) (bool, error) {
	lines, err := readLines(s.Path)
	if err != nil {
		return false, nil // file doesn't exist, nothing to delete
	}
	var out []string
	found := false
	for _, line := range lines {
		key, _ := parseLine(line)
		if key == name {
			found = true
			continue
		}
		out = append(out, line)
	}
	if !found {
		return false, nil
	}
	return true, os.WriteFile(s.Path, []byte(strings.Join(out, "\n")+"\n"), 0600)
}

// List returns all key names in the file.
// Returns a non-nil error only for real I/O failures (not missing files).
func (s *FileStore) List() ([]string, error) {
	entries, err := s.readAll()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for k := range entries {
		names = append(names, k)
	}
	return names, nil
}

func (s *FileStore) readAll() (map[string]string, error) {
	lines, err := readLines(s.Path)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for _, line := range lines {
		k, v := parseLine(line)
		if k != "" {
			m[k] = v
		}
	}
	return m, nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// parseLine splits "KEY=VALUE". Returns ("","") for comments and blank lines.
func parseLine(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", ""
	}
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(k), strings.TrimSpace(v)
}

// --- Keychain backend (cross-platform via go-keyring) ---

// KeychainGet reads a secret from the OS credential store.
func KeychainGet(account string) (string, error) {
	return keyring.Get(keychainService, account)
}

// KeychainSet writes a secret to the OS credential store.
func KeychainSet(account, value string) error {
	return keyring.Set(keychainService, account, value)
}

// KeychainDelete removes a secret from the OS credential store.
func KeychainDelete(account string) error {
	return keyring.Delete(keychainService, account)
}

// KeychainAccount builds the keychain account string for a scope and variable name.
func KeychainAccount(scope, keychainKey, name string) string {
	if keychainKey != "" {
		return scope + "/" + keychainKey + "/" + name
	}
	return scope + "/" + name
}
