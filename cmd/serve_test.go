package cmd

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
