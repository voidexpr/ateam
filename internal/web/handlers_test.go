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
		ProjectID:  "test-proj",
		Action:     action,
		Role:       role,
		Batch:      fmt.Sprintf("%s-%s", action, ts),
		StartedAt:  now.Add(-2 * time.Minute),
		StreamFile: streamRel,
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
		ProjectID:  "test-proj",
		Action:     runner.ActionReport,
		Role:       "security",
		Batch:      "report-" + ts,
		StartedAt:  now.Add(-2 * time.Minute),
		StreamFile: maliciousStream,
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
		ProjectID:  "test-proj",
		Action:     runner.ActionReport,
		Role:       "security",
		Batch:      "report-" + ts,
		StartedAt:  now.Add(-5 * time.Minute),
		StreamFile: streamRel,
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
