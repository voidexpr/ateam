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
		TaskGroup: "report-2026-04-01_10-00-00",
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
	ts := now.Format(runner.TimestampFormat)

	// Create logs directory and stream/exec files.
	var logsDir string
	switch action {
	case runner.ActionReport, runner.ActionRun:
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
		TaskGroup:  fmt.Sprintf("%s-%s", action, ts),
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
			TaskGroup: action + "-2026-04-01_10-00-00",
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

	// Create a database with a run whose StreamFile contains a path traversal.
	dbPath := filepath.Join(projectDir, "state.sqlite")
	db, err := calldb.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	now := time.Now()
	ts := now.Format(runner.TimestampFormat)
	// Craft a stream file path that tries to escape the project directory.
	maliciousStream := filepath.Join("..", "..", "etc", ts+"_report_stream.jsonl")

	callID, err := db.InsertCall(&calldb.Call{
		ProjectID:  "test-proj",
		Action:     runner.ActionReport,
		Role:       "security",
		TaskGroup:  "report-" + ts,
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

	// The isPathWithin check should block serving a file outside the project dir.
	if w.Code == http.StatusOK {
		t.Errorf("handleRunFile path traversal returned 200, expected non-200 (got %d)", w.Code)
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

	// Create a report file that DiscoverReports will find.
	roleDir := filepath.Join(projectDir, "roles", "security")
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "report.md"), []byte("# Security Report\nAll clear."), 0644); err != nil {
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
