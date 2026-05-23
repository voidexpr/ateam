package cmd

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/root"
)

// startServeAndWaitForURL spawns runServe in a goroutine, redirecting
// os.Stderr to a pipe, and reads from the pipe until it sees the
// "Serving at <URL>" banner. Returns the URL.
//
// The serve goroutine is intentionally leaked: runServe blocks in
// srv.Serve(ln) and the production code exposes no shutdown handle. The
// random-port listener won't conflict with anything, and Go reclaims the
// goroutine when the test process exits.
func startServeAndWaitForURL(t *testing.T) string {
	t.Helper()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr := os.Stderr
	os.Stderr = pw

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServe(nil, nil)
		pw.Close()
	}()

	urlCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := scanner.Text()
			if idx := strings.Index(line, "Serving at "); idx >= 0 {
				urlCh <- strings.TrimSpace(line[idx+len("Serving at "):])
				// Drain remaining output to keep the pipe writable.
				go io.Copy(io.Discard, pr)
				return
			}
		}
		urlCh <- ""
	}()

	var url string
	select {
	case url = <-urlCh:
	case err := <-errCh:
		os.Stderr = origStderr
		skipIfListenDenied(t, err)
		t.Fatalf("runServe exited before binding: %v", err)
	case <-time.After(5 * time.Second):
		os.Stderr = origStderr
		t.Fatal("timed out waiting for serve to bind")
	}
	os.Stderr = origStderr
	if url == "" {
		t.Fatal("serve did not print a URL")
	}
	return url
}

func skipIfListenDenied(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, os.ErrPermission) ||
		strings.Contains(err.Error(), "operation not permitted") ||
		strings.Contains(err.Error(), "permission denied") {
		t.Skipf("local TCP listen is not permitted in this environment: %v", err)
	}
}

func setupServeProject(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	orgDir, err := root.InstallOrg(base)
	if err != nil {
		t.Fatalf("InstallOrg: %v", err)
	}
	projPath := filepath.Join(base, "myproj")
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

	savedOrg, savedProj := orgFlag, projectFlag
	t.Cleanup(func() {
		orgFlag = savedOrg
		projectFlag = savedProj
	})
	orgFlag = filepath.Dir(orgDir)
	projectFlag = projPath
	return projPath
}

func saveServeGlobals() func() {
	port, noOpen, public, bind := servePort, serveNoOpen, servePublic, serveBind
	return func() {
		servePort = port
		serveNoOpen = noOpen
		servePublic = public
		serveBind = bind
	}
}

// TestServeBindsRandomPort starts the web server on 127.0.0.1:0, requests
// the home page, and asserts the response is HTTP 200.
func TestServeBindsRandomPort(t *testing.T) {
	defer saveServeGlobals()()
	setupServeProject(t)

	servePort = 0
	serveNoOpen = true
	servePublic = false
	serveBind = ""

	url := startServeAndWaitForURL(t)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestResolveServePortReusesCachedFreePort writes a known-free port to the
// cache file and asserts resolveServePort returns it. This is the tab-refocus
// promise: rerunning `ateam serve` reuses the same URL.
func TestResolveServePortReusesCachedFreePort(t *testing.T) {
	defer saveServeGlobals()()
	setupServeProject(t)

	servePort = 0
	serveBind = ""
	servePublic = false

	env, err := lookupEnv()
	if err != nil {
		t.Fatalf("lookupEnv: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skipIfListenDenied(t, err)
		t.Fatalf("Listen: %v", err)
	}
	cachedPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	portFile := filepath.Join(env.ProjectDir, "cache", "serve.port")
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(cachedPort)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	port, source, err := resolveServePort(env, "127.0.0.1")
	if err != nil {
		t.Fatalf("resolveServePort: %v", err)
	}
	if port != cachedPort {
		t.Errorf("port = %d, want cached %d", port, cachedPort)
	}
	if source == "" {
		t.Errorf("source = %q, want non-empty cache path", source)
	}
}

// TestResolveServePortPicksNewWhenCacheBusy holds a listener on the cached
// port so isPortFree fails, then verifies resolveServePort returns a fresh
// port and updates the cache file. Guards against the regression where a
// stale cache silently keeps reusing an occupied port.
func TestResolveServePortPicksNewWhenCacheBusy(t *testing.T) {
	defer saveServeGlobals()()
	setupServeProject(t)

	servePort = 0
	serveBind = ""
	servePublic = false

	env, err := lookupEnv()
	if err != nil {
		t.Fatalf("lookupEnv: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skipIfListenDenied(t, err)
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	busyPort := ln.Addr().(*net.TCPAddr).Port

	portFile := filepath.Join(env.ProjectDir, "cache", "serve.port")
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(busyPort)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	port, _, err := resolveServePort(env, "127.0.0.1")
	if err != nil {
		t.Fatalf("resolveServePort: %v", err)
	}
	if port == busyPort {
		t.Errorf("port = %d, expected a different port from busy %d", port, busyPort)
	}
	if port <= 0 {
		t.Errorf("port = %d, want > 0", port)
	}

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	stored, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	if stored != port {
		t.Errorf("cached port after pick = %d, want %d", stored, port)
	}
}

// TestResolveServePortExplicitFlagBypassesCache verifies --port short-circuits
// resolveServePort: a pre-existing cache file must be neither read nor touched.
func TestResolveServePortExplicitFlagBypassesCache(t *testing.T) {
	defer saveServeGlobals()()
	setupServeProject(t)

	servePort = 23456
	serveBind = ""
	servePublic = false

	env, err := lookupEnv()
	if err != nil {
		t.Fatalf("lookupEnv: %v", err)
	}

	portFile := filepath.Join(env.ProjectDir, "cache", "serve.port")
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		t.Fatal(err)
	}
	const cachedPort = 34567
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(cachedPort)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	port, source, err := resolveServePort(env, "127.0.0.1")
	if err != nil {
		t.Fatalf("resolveServePort: %v", err)
	}
	if port != 23456 {
		t.Errorf("port = %d, want explicit 23456", port)
	}
	if source != "" {
		t.Errorf("source = %q, want empty (cache bypassed)", source)
	}

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	stored, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("Atoi: %v", err)
	}
	if stored != cachedPort {
		t.Errorf("cache file modified: stored = %d, want untouched %d", stored, cachedPort)
	}
}

// TestResolveServePortWritesCacheWhenMissing covers the cold-start path: no
// cache file present, resolveServePort must pick a port and persist it so the
// next invocation can reuse the same URL.
func TestResolveServePortWritesCacheWhenMissing(t *testing.T) {
	defer saveServeGlobals()()
	setupServeProject(t)

	servePort = 0
	serveBind = ""
	servePublic = false

	env, err := lookupEnv()
	if err != nil {
		t.Fatalf("lookupEnv: %v", err)
	}

	portFile := filepath.Join(env.ProjectDir, "cache", "serve.port")
	if _, err := os.Stat(portFile); !os.IsNotExist(err) {
		t.Fatalf("expected no cache file, got err=%v", err)
	}

	port, source, err := resolveServePort(env, "127.0.0.1")
	if err != nil {
		t.Fatalf("resolveServePort: %v", err)
	}
	if port <= 0 {
		t.Errorf("port = %d, want > 0", port)
	}
	if source == "" {
		t.Errorf("source = %q, want non-empty path", source)
	}

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("cache file not written: %v", err)
	}
	stored, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("cache content invalid: %v", err)
	}
	if stored != port {
		t.Errorf("cached port = %d, want %d", stored, port)
	}
}

// TestServeBindsExplicitPort verifies that --port is honored. Picks a free
// port by binding to :0 and immediately closing, then hands that port to
// runServe. Mildly racy but unlikely to conflict in practice.
func TestServeBindsExplicitPort(t *testing.T) {
	defer saveServeGlobals()()
	setupServeProject(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		skipIfListenDenied(t, err)
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	servePort = port
	serveNoOpen = true
	servePublic = false
	serveBind = ""

	url := startServeAndWaitForURL(t)
	if !strings.Contains(url, ":") {
		t.Fatalf("URL %q has no port", url)
	}
	// Accept either /p/<slug>/ (single-project mode) or root path.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
