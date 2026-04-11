package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/calldb"
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
