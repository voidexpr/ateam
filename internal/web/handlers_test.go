package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/runner"
)

// newTestServer creates a minimal Server for handler testing.
// It uses the real embedded templates and a single project entry.
func newTestServer(t *testing.T, projectDir string) *Server {
	t.Helper()

	tmpl, err := template.New("").Funcs(funcMap()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}

	s := &Server{
		templates:  tmpl,
		md:         newMarkdown(),
		singleMode: true,
		projects: []ProjectEntry{{
			Name:       "testproj",
			Slug:       "testproj",
			ProjectDir: projectDir,
			OrgDir:     "",
			SourceDir:  projectDir,
		}},
	}
	return s
}

// newTestMux wires up the server's handlers to a ServeMux with the same
// route patterns used in ListenAndServe.
func newTestMux(s *Server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{project}/", s.handleOverview)
	mux.HandleFunc("GET /p/{project}/cost", s.handleCost)
	mux.HandleFunc("GET /p/{project}/runs", s.handleRuns)
	mux.HandleFunc("GET /p/{project}/runs/{id}", s.handleRun)
	mux.HandleFunc("GET /p/{project}/runs/{id}/runtime/{name...}", s.handleRunRuntimeFile)
	mux.HandleFunc("GET /p/{project}/runs/{id}/{file}", s.handleRunFile)
	mux.HandleFunc("GET /p/{project}/reports/{role}", s.handleReport)
	mux.Handle("GET /p/{project}/review", s.handleReview())
	mux.Handle("GET /p/{project}/verify", s.handleVerify())
	mux.HandleFunc("GET /p/{project}/prompts", s.handlePrompts)
	return mux
}

func TestHandleOverviewReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)

	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleOverview status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandleOverviewNotFound(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)

	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/nonexistent/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleOverview for unknown project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleOverviewWithReview(t *testing.T) {
	projectDir := t.TempDir()

	// Create a review.md so the overview detects it.
	supervisorDir := filepath.Join(projectDir, "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(supervisorDir, "review.md"), []byte("# Review\nSome review content"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleOverviewWithDatabase(t *testing.T) {
	projectDir := t.TempDir()

	// Create a database with some runs.
	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	callID, err := db.InsertCall(&calldb.Call{
		ProjectID: "test-proj",
		Action:    "report",
		Role:      "security",
		Batch:     "report-2026-04-01_10-00-00",
		StartedAt: now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(callID, &calldb.CallResult{
		EndedAt:    now.Add(-1 * time.Minute),
		DurationMS: 60000,
		CostUSD:    0.05,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	db.Close()

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "testproj") {
		t.Error("expected project name in response body")
	}
	if !strings.Contains(body, "security") {
		t.Error("expected seeded role 'security' in overview body")
	}
}

func TestHandleOverviewShowAll(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)

	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/?all=1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleCostReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)

	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/cost", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleCost status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandleCostNotFound(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)

	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/nonexistent/cost", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleCost for unknown project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// seedRun inserts a completed run into the database and creates the corresponding
// stream and exec files on disk. Returns the run ID.
func seedRun(t *testing.T, projectDir string, action, role string) int64 {
	t.Helper()

	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	now := time.Now()
	ts := now.Format(display.TimestampFormat)

	// Create logs directory and stream/exec files.
	var logsDir string
	switch action {
	case runner.ActionReport, runner.ActionExec:
		logsDir = filepath.Join("roles", role, "history")
	default:
		logsDir = filepath.Join("supervisor", "history")
	}
	absLogsDir := filepath.Join(projectDir, logsDir)
	if err := os.MkdirAll(absLogsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	streamBase := ts + "_" + action + "_stream.jsonl"
	execBase := ts + "_" + action + "_exec.md"
	streamRel := filepath.Join(logsDir, streamBase)

	// Write stream file (minimal content).
	if err := os.WriteFile(filepath.Join(absLogsDir, streamBase), []byte(`{"type":"init"}`+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile stream: %v", err)
	}
	// Write exec file.
	if err := os.WriteFile(filepath.Join(absLogsDir, execBase), []byte("# Exec Report\nSome execution details."), 0644); err != nil {
		t.Fatalf("WriteFile exec: %v", err)
	}

	callID, err := db.InsertCall(&calldb.Call{
		ProjectID: "test-proj",
		Action:    action,
		Role:      role,
		Batch:     fmt.Sprintf("%s-%s", action, ts),
		StartedAt: now.Add(-2 * time.Minute),
		AgentFile: streamRel,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(callID, &calldb.CallResult{
		EndedAt:    now.Add(-1 * time.Minute),
		DurationMS: 60000,
		CostUSD:    0.05,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	return callID
}

// seedRunNewLayout inserts a run row using the post-rollout
// logs/<exec_id>/{agent.jsonl,cmd.md,bundle.jsonl,settings.json,prompt.md}
// layout, with a runtime/<exec_id>/report.md sidecar. Used by tests
// covering bundle/settings/runtime surfacing in serve.
func seedRunNewLayout(t *testing.T, projectDir string) int64 {
	t.Helper()

	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	callID, err := db.InsertCall(&calldb.Call{
		ProjectID: "test-proj",
		Action:    runner.ActionReport,
		Role:      "security",
		Batch:     "test-batch",
		StartedAt: time.Now().Add(-2 * time.Minute),
		// AgentFile filled in after we know the directory layout below.
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}

	logsRel := filepath.Join("logs", fmt.Sprintf("%d", callID))
	logsDir := filepath.Join(projectDir, logsRel)
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		t.Fatalf("MkdirAll logs: %v", err)
	}
	for name, body := range map[string]string{
		"agent.jsonl":   `{"type":"init"}` + "\n",
		"cmd.md":        "# Runtime\n",
		"bundle.jsonl":  `{"ts":1,"source":"bundle","kind":"bundle_start"}` + "\n",
		"settings.json": `{"key":"value"}`,
		"prompt.md":     "render me",
		"stderr.out":    "warning",
	} {
		if err := os.WriteFile(filepath.Join(logsDir, name), []byte(body), 0600); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	runtimeDir := filepath.Join(projectDir, "runtime", fmt.Sprintf("%d", callID))
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		t.Fatalf("MkdirAll runtime: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "report.md"), []byte("# Report\nbody"), 0600); err != nil {
		t.Fatalf("WriteFile runtime/report: %v", err)
	}

	if err := db.UpdateStreamFile(callID, filepath.Join(logsRel, "agent.jsonl")); err != nil {
		t.Fatalf("UpdateStreamFile: %v", err)
	}
	if err := db.UpdateCall(callID, &calldb.CallResult{
		EndedAt:    time.Now().Add(-1 * time.Minute),
		DurationMS: 60000,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	return callID
}

func TestHandleRunFileBundleReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRunNewLayout(t, projectDir)

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/bundle", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("bundle status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "bundle_start") {
		t.Error("expected bundle event content in response")
	}
}

func TestHandleRunFileSettingsReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRunNewLayout(t, projectDir)

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/settings", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("settings status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "key") {
		t.Error("expected settings content in response")
	}
}

func TestHandleRunRuntimeFileReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRunNewLayout(t, projectDir)

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/runtime/report.md", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("runtime status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Report") {
		t.Error("expected runtime file content in response")
	}
}

func TestHandleRunRuntimeFilePathTraversal(t *testing.T) {
	// Two-layered guard: net/http's mux pre-normalizes "../" out of the
	// URL (returning a 307 to the cleaned path), and the handler's
	// isPathWithin check rejects anything that lands outside
	// runtime/<exec_id>/. This test exercises both: a "../"-style URL is
	// expected to never reach the underlying file, and a crafted name
	// passed past the mux is rejected by the handler.
	projectDir := t.TempDir()
	callID := seedRunNewLayout(t, projectDir)
	const canary = "TOPSECRETcanaryDEADBEEFdonotleak"
	if err := os.WriteFile(filepath.Join(projectDir, "secret.md"), []byte(canary), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	// Mux-layer guard: ../-bearing URL is redirected and the canary never
	// appears.
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/runtime/../../secret.md", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), canary) {
		t.Errorf("traversal leaked outside-runtime file content: %s", w.Body.String())
	}

	// Handler-layer guard: bypass the mux and invoke the handler directly
	// with a malicious name that *would* escape runtime/<id>/.
	direct := httptest.NewRequest("GET", "/", nil)
	direct.SetPathValue("project", "testproj")
	direct.SetPathValue("id", fmt.Sprintf("%d", callID))
	direct.SetPathValue("name", "../../secret.md")
	dw := httptest.NewRecorder()
	s.handleRunRuntimeFile(dw, direct)
	if dw.Code != http.StatusNotFound {
		t.Errorf("direct traversal not rejected; status %d body %s", dw.Code, dw.Body.String())
	}
	if strings.Contains(dw.Body.String(), canary) {
		t.Error("direct traversal leaked canary")
	}
}

func TestHandleRunListsNewFiles(t *testing.T) {
	// The run detail page should link to bundle.jsonl, settings.json, and
	// every file in runtime/<id>/ for a new-layout run.
	projectDir := t.TempDir()
	callID := seedRunNewLayout(t, projectDir)

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	body := w.Body.String()
	for _, want := range []string{
		fmt.Sprintf(`/p/testproj/runs/%d/bundle`, callID),
		fmt.Sprintf(`/p/testproj/runs/%d/settings`, callID),
		fmt.Sprintf(`/p/testproj/runs/%d/runtime/report.md`, callID),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("run page missing link %q", want)
		}
	}
}

func TestHandleCostWithDatabase(t *testing.T) {
	projectDir := t.TempDir()

	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()

	// Insert a couple of calls with costs.
	for _, action := range []string{"report", "review"} {
		callID, err := db.InsertCall(&calldb.Call{
			ProjectID: "test-proj",
			Action:    action,
			Role:      "security",
			Batch:     action + "-2026-04-01_10-00-00",
			StartedAt: now.Add(-5 * time.Minute),
		})
		if err != nil {
			t.Fatalf("InsertCall: %v", err)
		}
		if err := db.UpdateCall(callID, &calldb.CallResult{
			EndedAt:      now.Add(-4 * time.Minute),
			DurationMS:   60000,
			CostUSD:      0.10,
			InputTokens:  3000,
			OutputTokens: 2000,
		}); err != nil {
			t.Fatalf("UpdateCall: %v", err)
		}
	}
	db.Close()

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/cost", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "testproj") {
		t.Error("expected project name in cost page body")
	}
	// Two seeded calls at $0.10 each → total $0.20. fmtCost renders "$0.20".
	if !strings.Contains(body, "0.20") {
		t.Error("expected total cost '0.20' in cost page body")
	}
}

// --- handleRuns tests ---

func TestHandleRunsReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/testproj/runs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleRuns status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandleRunsNotFound(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/runs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleRuns for unknown project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleRunsWithDatabase(t *testing.T) {
	projectDir := t.TempDir()
	seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/runs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "security") {
		t.Error("expected role name 'security' in runs page body")
	}
}

// --- handleRun tests ---

func TestHandleRunReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleRun status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "testproj") {
		t.Error("expected project name in run detail body")
	}
	if !strings.Contains(body, "security") {
		t.Error("expected seeded role 'security' in run detail body")
	}
}

func TestHandleRunNotFoundBadID(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/testproj/runs/abc", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleRun bad id status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleRunNotFoundMissingID(t *testing.T) {
	projectDir := t.TempDir()
	seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/runs/99999", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleRun missing id status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleRunFile tests ---

func TestHandleRunFileExecReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/exec", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleRunFile exec status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Exec") {
		t.Error("expected 'Exec' in run file page")
	}
}

func TestHandleRunFileUnknownType(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/unknown", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleRunFile unknown type status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleRunFilePathTraversal(t *testing.T) {
	projectDir := t.TempDir()

	// Place a REAL, readable file OUTSIDE the project directory. For the "exec"
	// file type with a legacy stream path, handleRunFile derives the served file
	// by replacing the "_stream.jsonl" suffix with "_exec.md" — so the secret
	// lives at <outside>/secret_exec.md and the crafted stream points at
	// <outside>/secret_stream.jsonl.
	outsideDir := t.TempDir()
	const canary = "TOPSECRETcanaryDEADBEEFdonotleak"
	secretFile := filepath.Join(outsideDir, "secret_exec.md")
	if err := os.WriteFile(secretFile, []byte(canary), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Sanity-check the fixture: the file must be readable, otherwise an
	// incidental read failure (not the guard) would make this test pass.
	if data, err := os.ReadFile(secretFile); err != nil || string(data) != canary {
		t.Fatalf("outside file not readable as expected: %v", err)
	}

	// Build a project-relative stream path (containing "..") that resolves to
	// the outside directory.
	streamTarget := filepath.Join(outsideDir, "secret_stream.jsonl")
	maliciousStream, err := filepath.Rel(projectDir, streamTarget)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if !strings.Contains(maliciousStream, "..") {
		t.Fatalf("expected a traversal path, got %q", maliciousStream)
	}

	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now()
	ts := now.Format(display.TimestampFormat)
	callID, err := db.InsertCall(&calldb.Call{
		ProjectID: "test-proj",
		Action:    runner.ActionReport,
		Role:      "security",
		Batch:     "report-" + ts,
		StartedAt: now.Add(-2 * time.Minute),
		AgentFile: maliciousStream,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(callID, &calldb.CallResult{
		EndedAt:    now.Add(-1 * time.Minute),
		DurationMS: 60000,
		CostUSD:    0.01,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	db.Close()

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/exec", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// The isPathWithin guard must block serving a file outside the project dir.
	// Because the target file genuinely exists and is readable, a 200 here can
	// only mean the guard was bypassed — this is NOT an incidental file-absence
	// 404. Removing the guard makes os.ReadFile succeed and serve the canary.
	if w.Code == http.StatusOK {
		t.Errorf("handleRunFile path traversal returned 200, expected blocked (got %d)", w.Code)
	}
	if strings.Contains(w.Body.String(), canary) {
		t.Errorf("response leaked the contents of an out-of-project file")
	}
}

func TestHandleRunFileNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/nonexistent/runs/%d/exec", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleRunFile bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleReport tests ---

func TestHandleReportReturnsOK(t *testing.T) {
	projectDir := t.TempDir()

	// Create a report file that DiscoverReports will find (v1 flat layout).
	reportDir := filepath.Join(projectDir, "shared", "report")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "security.md"), []byte("# Security Report\nAll clear."), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/reports/security", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleReport status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Security Report") {
		t.Error("expected rendered markdown content 'Security Report' in response body")
	}
}

func TestHandleReportNotFoundMissingRole(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/testproj/reports/nonexistent", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleReport missing role status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleReportNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/reports/security", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleReport bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleReview tests ---

func TestHandleReviewReturnsOK(t *testing.T) {
	projectDir := t.TempDir()

	// Create a review.md file.
	supervisorDir := filepath.Join(projectDir, "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(supervisorDir, "review.md"), []byte("# Supervisor Review\nEverything looks good."), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/review", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleReview status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Supervisor Review") {
		t.Error("expected rendered markdown content 'Supervisor Review' in response body")
	}
}

func TestHandleReviewNoReviewFile(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/testproj/review", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should still return 200 — the handler renders a page even without review.md.
	if w.Code != http.StatusOK {
		t.Errorf("handleReview no file status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleReviewNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/review", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleReview bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleVerify tests ---

func TestHandleVerifyReturnsOK(t *testing.T) {
	projectDir := t.TempDir()

	supervisorDir := filepath.Join(projectDir, "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(supervisorDir, "verify.md"), []byte("# Code Verification\nAll checks passed."), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", "/p/testproj/verify", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleVerify status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Code Verification") {
		t.Error("expected rendered markdown content 'Code Verification' in response body")
	}
}

func TestHandleVerifyNoFile(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/testproj/verify", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	// Should still return 200 — the handler renders a page even without verify.md.
	if w.Code != http.StatusOK {
		t.Errorf("handleVerify no file status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleVerifyBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/verify", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleVerify bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handlePrompts tests ---

func TestHandlePromptsReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/testproj/prompts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handlePrompts status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandlePromptsBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMux(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/prompts", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handlePrompts bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// newBrokenDBServer creates a test server whose DB path points to a directory,
// causing calldb.OpenIfExists to fail with a deterministic error.
func newBrokenDBServer(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	projectDir := t.TempDir()
	dbPath := filepath.Join(projectDir, "state.sqlite")
	if err := os.Mkdir(dbPath, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := newTestServer(t, projectDir)
	return s, newTestMux(s)
}

func TestHandleOverviewDBError(t *testing.T) {
	s, mux := newBrokenDBServer(t)
	_ = s
	req := httptest.NewRequest("GET", "/p/testproj/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("handleOverview DB error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleRunsDBError(t *testing.T) {
	_, mux := newBrokenDBServer(t)
	req := httptest.NewRequest("GET", "/p/testproj/runs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("handleRuns DB error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleRunDBError(t *testing.T) {
	_, mux := newBrokenDBServer(t)
	req := httptest.NewRequest("GET", "/p/testproj/runs/1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("handleRun DB error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleRunFileDBError(t *testing.T) {
	_, mux := newBrokenDBServer(t)
	req := httptest.NewRequest("GET", "/p/testproj/runs/1/exec", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("handleRunFile DB error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleCostDBError(t *testing.T) {
	_, mux := newBrokenDBServer(t)
	req := httptest.NewRequest("GET", "/p/testproj/cost", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("handleCost DB error status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestHandleRunFileLogsShowsTimestamps(t *testing.T) {
	projectDir := t.TempDir()

	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open calldb: %v", err)
	}
	defer db.Close()

	now := time.Now()
	ts := now.Format(display.TimestampFormat)

	logsDir := filepath.Join("roles", "security", "history")
	absLogsDir := filepath.Join(projectDir, logsDir)
	if err := os.MkdirAll(absLogsDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	streamBase := ts + "_report_stream.jsonl"
	streamRel := filepath.Join(logsDir, streamBase)
	streamLines := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sid-web","model":"claude-sonnet","cwd":"/tmp"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"web logs output"}]}}`,
		`{"type":"result","total_cost_usd":0.01,"duration_ms":60000,"num_turns":1,"usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0}}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(absLogsDir, streamBase), []byte(streamLines), 0644); err != nil {
		t.Fatalf("WriteFile stream: %v", err)
	}

	callID, err := db.InsertCall(&calldb.Call{
		ProjectID: "test-proj",
		Action:    runner.ActionReport,
		Role:      "security",
		Batch:     "report-" + ts,
		StartedAt: now.Add(-5 * time.Minute),
		AgentFile: streamRel,
	})
	if err != nil {
		t.Fatalf("InsertCall: %v", err)
	}
	if err := db.UpdateCall(callID, &calldb.CallResult{
		EndedAt:    now,
		DurationMS: 60000,
		CostUSD:    0.01,
	}); err != nil {
		t.Fatalf("UpdateCall: %v", err)
	}
	db.Close()

	s := newTestServer(t, projectDir)
	mux := newTestMux(s)
	req := httptest.NewRequest("GET", fmt.Sprintf("/p/testproj/runs/%d/logs", callID), nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleRunFile logs status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Started:") {
		t.Errorf("expected Started timestamp in logs HTML (session start plumbing):\n%s", body)
	}
	if !strings.Contains(body, "Ended:") {
		t.Errorf("expected Ended timestamp in logs HTML (session start plumbing):\n%s", body)
	}
}

// TestServeHistoryFilePathTraversal exercises the isPathWithin guard inside
// serveHistoryFile. The handler accepts a {file} URL segment and joins it with
// the role/supervisor history dir; a filename containing ".." that resolves
// outside histDir must be blocked. The path is fed via direct method call so
// the test is not subject to http.ServeMux's path-cleaning of ".." segments.
func TestServeHistoryFilePathTraversal(t *testing.T) {
	projectDir := t.TempDir()
	histDir := filepath.Join(projectDir, "roles", "security", "history")
	if err := os.MkdirAll(histDir, 0755); err != nil {
		t.Fatalf("MkdirAll histDir: %v", err)
	}

	// Real, readable file OUTSIDE the project directory. If the guard is
	// bypassed, os.ReadFile would succeed and leak the canary into the
	// rendered page — so a 200 with no canary is not a meaningful pass.
	outsideDir := t.TempDir()
	const canary = "TOPSECRETcanaryHISTORYdonotleak"
	secretFile := filepath.Join(outsideDir, "secret.md")
	if err := os.WriteFile(secretFile, []byte(canary), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if data, err := os.ReadFile(secretFile); err != nil || string(data) != canary {
		t.Fatalf("outside file not readable as expected: %v", err)
	}

	// Build a histDir-relative filename (containing "..") that resolves to the
	// outside secret file.
	maliciousFile, err := filepath.Rel(histDir, secretFile)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if !strings.Contains(maliciousFile, "..") {
		t.Fatalf("expected a traversal path, got %q", maliciousFile)
	}

	s := newTestServer(t, projectDir)
	pe := &s.projects[0]
	req := httptest.NewRequest("GET", "/p/testproj/reports/security/history/"+maliciousFile, nil)
	w := httptest.NewRecorder()
	s.serveHistoryFile(w, req, pe, histDir, maliciousFile, "security", "reports")

	if w.Code == http.StatusOK {
		t.Errorf("serveHistoryFile path traversal returned 200, expected blocked (got %d)", w.Code)
	}
	if strings.Contains(w.Body.String(), canary) {
		t.Errorf("response leaked the contents of an out-of-project file")
	}
}

// TestHandleCodeSessionFilePathTraversal exercises the isPathWithin guard
// inside handleCodeSessionFile. The {session} URL segment is joined with
// "shared/code/" and the {file} segment; a {session} value containing ".."
// that resolves outside the project must be blocked. r.SetPathValue feeds the
// crafted values directly so http.ServeMux's path-cleaning does not strip
// them.
func TestHandleCodeSessionFilePathTraversal(t *testing.T) {
	projectDir := t.TempDir()

	// Real, readable file OUTSIDE the project directory. Naming it "*.md" is
	// required because the handler rejects non-.md filenames before the
	// guard fires.
	outsideDir := t.TempDir()
	const canary = "TOPSECRETcanaryCODESESSIONdonotleak"
	secretFile := filepath.Join(outsideDir, "secret.md")
	if err := os.WriteFile(secretFile, []byte(canary), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if data, err := os.ReadFile(secretFile); err != nil || string(data) != canary {
		t.Fatalf("outside file not readable as expected: %v", err)
	}

	// Build a {session} value such that filepath.Join(projectDir,"shared",
	// "code", session) resolves to outsideDir. The fileName check rejects
	// "/" and ".." in the filename, so the traversal must live in session.
	codeBase := filepath.Join(projectDir, "shared", "code")
	maliciousSession, err := filepath.Rel(codeBase, outsideDir)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if !strings.Contains(maliciousSession, "..") {
		t.Fatalf("expected a traversal session, got %q", maliciousSession)
	}

	s := newTestServer(t, projectDir)
	req := httptest.NewRequest("GET", "/p/testproj/code/x/secret.md", nil)
	req.SetPathValue("project", "testproj")
	req.SetPathValue("session", maliciousSession)
	req.SetPathValue("file", "secret.md")
	w := httptest.NewRecorder()
	s.handleCodeSessionFile(w, req)

	if w.Code == http.StatusOK {
		t.Errorf("handleCodeSessionFile path traversal returned 200, expected blocked (got %d)", w.Code)
	}
	if strings.Contains(w.Body.String(), canary) {
		t.Errorf("response leaked the contents of an out-of-project file")
	}
}

// newTestMuxAll wires the second-tier routes that newTestMux omits — used by
// the sessions / code-sessions / report-history smoke tests.
func newTestMuxAll(s *Server) *http.ServeMux {
	mux := newTestMux(s)
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /p/{project}/reports", s.handleReports)
	mux.HandleFunc("GET /p/{project}/reports/{role}/history/{file}", s.handleReportHistory)
	mux.HandleFunc("GET /p/{project}/sessions", s.handleSessions)
	mux.HandleFunc("GET /p/{project}/sessions/{batch}", s.handleSessionDetail)
	mux.HandleFunc("GET /p/{project}/code", s.handleCodeSessions)
	mux.HandleFunc("GET /p/{project}/code/{session}", s.handleCodeSessionDetail)
	return mux
}

// --- handleSessions tests ---

func TestHandleSessionsReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/testproj/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleSessions status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	// Empty-state anchor — no DB so buildSessions returns nothing.
	if body := w.Body.String(); !strings.Contains(body, "No sessions") {
		t.Errorf("expected empty-state 'No sessions' in sessions page body")
	}
}

func TestHandleSessionsWithDatabase(t *testing.T) {
	projectDir := t.TempDir()
	seedRun(t, projectDir, runner.ActionReport, "security")

	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)
	req := httptest.NewRequest("GET", "/p/testproj/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Sessions") {
		t.Error("expected page title 'Sessions' in body")
	}
	// seedRun uses Batch "report-<ts>" — confirm the seeded batch row reaches the page.
	if !strings.Contains(body, "report-") {
		t.Error("expected seeded batch (prefixed 'report-') in sessions body")
	}
}

func TestHandleSessionsNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/sessions", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleSessions bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleSessionDetail tests ---

func TestHandleSessionDetailReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	callID := seedRun(t, projectDir, runner.ActionReport, "security")

	// seedRun uses Batch "report-<ts>" — pull the row back so the test does
	// not have to reconstruct the exact format.
	db, err := calldb.Open(filepath.Join(projectDir, "state.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	row, err := db.GetRunByID(callID)
	db.Close()
	if err != nil || row == nil {
		t.Fatalf("GetRunByID(%d): row=%v err=%v", callID, row, err)
	}
	batch := row.Batch

	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)
	req := httptest.NewRequest("GET", "/p/testproj/sessions/"+batch, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleSessionDetail status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, batch) {
		t.Errorf("expected batch %q in session detail body", batch)
	}
	if !strings.Contains(body, "security") {
		t.Error("expected seeded role 'security' in session detail body")
	}
}

func TestHandleSessionDetailUnknownBatch(t *testing.T) {
	// handleSessionDetail does not 404 on an unknown batch — it renders an
	// empty session_detail page with the requested batch as the title. Lock
	// in that actual behavior so the test fails loudly if it changes.
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/testproj/sessions/no-such-batch", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleSessionDetail unknown-batch status = %d, want %d (renders empty page)", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "no-such-batch") {
		t.Error("expected requested batch echoed in empty session detail page")
	}
}

func TestHandleSessionDetailNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/sessions/anything", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleSessionDetail bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleCodeSessions tests ---

func TestHandleCodeSessionsReturnsOK(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/testproj/code", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleCodeSessions status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "Code Sessions") {
		t.Error("expected page heading 'Code Sessions' in body")
	}
}

func TestHandleCodeSessionsWithSession(t *testing.T) {
	projectDir := t.TempDir()

	// Seed a legacy timestamp-named code session directory so scanCodeSessions
	// picks it up without needing a DB row.
	tsName := "2026-03-19_00-35-57"
	codeDir := filepath.Join(projectDir, "shared", "code", tsName)
	if err := os.MkdirAll(codeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "execution_report.md"), []byte("# Report"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)
	req := httptest.NewRequest("GET", "/p/testproj/code", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, tsName) {
		t.Errorf("expected seeded session %q in code sessions body", tsName)
	}
}

func TestHandleCodeSessionsNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/code", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleCodeSessions bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleCodeSessionDetail tests ---

func TestHandleCodeSessionDetailReturnsOK(t *testing.T) {
	projectDir := t.TempDir()

	tsName := "2026-03-19_00-35-57"
	codeDir := filepath.Join(projectDir, "shared", "code", tsName)
	if err := os.MkdirAll(codeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "execution_report.md"), []byte("# Report"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codeDir, "task1_code_prompt.md"), []byte("# Task"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)
	req := httptest.NewRequest("GET", "/p/testproj/code/"+tsName, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleCodeSessionDetail status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, tsName) {
		t.Errorf("expected session name %q in code session detail body", tsName)
	}
	if !strings.Contains(body, "execution_report.md") {
		t.Error("expected execution_report.md listed in code session detail body")
	}
}

func TestHandleCodeSessionDetailNotFoundUnknown(t *testing.T) {
	// handleCodeSessionDetail explicitly 404s when the canonical dir does
	// not exist (os.Stat fails) — this is the unknown-session case.
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/testproj/code/no-such-session", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleCodeSessionDetail unknown session status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleCodeSessionDetailNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/code/anything", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleCodeSessionDetail bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

// --- handleHome / handleReports / handleReportHistory smoke tests ---

func TestHandleHomeSingleModeRedirects(t *testing.T) {
	// newTestServer sets singleMode=true — handleHome 302-redirects to the
	// sole project's overview in that case.
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Errorf("handleHome singleMode status = %d, want %d", w.Code, http.StatusFound)
	}
	if loc := w.Header().Get("Location"); loc != "/p/testproj/" {
		t.Errorf("handleHome singleMode Location = %q, want /p/testproj/", loc)
	}
}

func TestHandleHomeMultiProjectListsProjects(t *testing.T) {
	// In multi-project mode handleHome renders home.html — assert 200 and
	// the page heading anchor.
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	s.singleMode = false
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleHome multi-project status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(w.Body.String(), "Projects") {
		t.Error("expected 'Projects' heading anchor in home page body")
	}
}

func TestHandleReportsReturnsOK(t *testing.T) {
	projectDir := t.TempDir()

	// Seed a single report so the page renders the populated branch in
	// addition to its always-present heading.
	reportDir := filepath.Join(projectDir, "shared", "report")
	if err := os.MkdirAll(reportDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "security.md"), []byte("# Security"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)
	req := httptest.NewRequest("GET", "/p/testproj/reports", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleReports status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Role Reports") {
		t.Error("expected 'Role Reports' heading in reports body")
	}
	if !strings.Contains(body, "security") {
		t.Error("expected seeded role 'security' in reports body")
	}
}

func TestHandleReportsEmpty(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/testproj/reports", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleReports empty status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "No reports") {
		t.Error("expected empty-state 'No reports' in reports body")
	}
}

func TestHandleReportsNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/reports", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleReports bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleReportHistoryReturnsOK(t *testing.T) {
	projectDir := t.TempDir()

	histDir := filepath.Join(projectDir, "roles", "security", "history")
	if err := os.MkdirAll(histDir, 0755); err != nil {
		t.Fatal(err)
	}
	histFile := "2026-03-14_00-20-28.report.md"
	if err := os.WriteFile(filepath.Join(histDir, histFile), []byte("# Archived Security Report"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)
	req := httptest.NewRequest("GET", "/p/testproj/reports/security/history/"+histFile, nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("handleReportHistory status = %d, want %d (body: %s)", w.Code, http.StatusOK, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Archived Security Report") {
		t.Error("expected rendered archived markdown content in body")
	}
	if !strings.Contains(body, "security") {
		t.Error("expected role 'security' in history detail body")
	}
}

func TestHandleReportHistoryNotFoundUnknownRole(t *testing.T) {
	// handleReportHistory 404s when the role does not appear in discoverRoles
	// — unknown-role-shaped URLs must not reach serveHistoryFile.
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/testproj/reports/totally-not-a-role/history/some.md", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleReportHistory unknown role status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHandleReportHistoryNotFoundBadProject(t *testing.T) {
	projectDir := t.TempDir()
	s := newTestServer(t, projectDir)
	mux := newTestMuxAll(s)

	req := httptest.NewRequest("GET", "/p/nonexistent/reports/security/history/some.md", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("handleReportHistory bad project status = %d, want %d", w.Code, http.StatusNotFound)
	}
}
