package secret

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Backend identifies a secret storage backend.
type Backend string

const (
	BackendFile     Backend = "file"
	BackendKeychain Backend = "keychain"

	keychainService = "ateam"
)

// DefaultBackend returns the preferred backend for the current OS.
func DefaultBackend() Backend {
	if runtime.GOOS == "darwin" {
		return BackendKeychain
	}
	return BackendFile
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

// Get returns the value for name, or ("", false) if not found.
func (s *FileStore) Get(name string) (string, bool) {
	entries, _ := s.readAll()
	val, ok := entries[name]
	return val, ok
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
func (s *FileStore) List() []string {
	entries, _ := s.readAll()
	var names []string
	for k := range entries {
		names = append(names, k)
	}
	return names
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
	return strings.TrimSpace(k), v
}

// --- Keychain backend (macOS) ---

// KeychainGet reads a secret from macOS Keychain.
func KeychainGet(account string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("keychain is only available on macOS")
	}
	out, err := exec.Command("security", "find-generic-password",
		"-s", keychainService, "-a", account, "-w").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// KeychainSet writes a secret to macOS Keychain.
func KeychainSet(account, value string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("keychain is only available on macOS")
	}
	// Delete first to avoid "already exists" error.
	exec.Command("security", "delete-generic-password",
		"-s", keychainService, "-a", account).Run()
	return exec.Command("security", "add-generic-password",
		"-s", keychainService, "-a", account, "-w", value).Run()
}

// KeychainDelete removes a secret from macOS Keychain.
func KeychainDelete(account string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("keychain is only available on macOS")
	}
	return exec.Command("security", "delete-generic-password",
		"-s", keychainService, "-a", account).Run()
}

// KeychainAccount builds the keychain account string for a scope and variable name.
func KeychainAccount(scope, keychainKey, name string) string {
	if keychainKey != "" {
		return scope + "/" + keychainKey + "/" + name
	}
	return scope + "/" + name
}
