// Package web implements the web UI server and HTTP handlers for browsing project runs and reports.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/yuin/goldmark"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// ProjectEntry holds info about a discovered project for listing.
type ProjectEntry struct {
	Name       string // display name from config
	Slug       string // URL-safe identifier
	ProjectDir string
	OrgDir     string
	SourceDir  string
	db         *calldb.CallDB // cached; may be nil
}

// SupervisorPath returns ProjectDir/supervisor/<name>.
func (pe *ProjectEntry) SupervisorPath(name string) string {
	return filepath.Join(pe.ProjectDir, "supervisor", name)
}

// SupervisorHistoryDir returns ProjectDir/supervisor/history.
func (pe *ProjectEntry) SupervisorHistoryDir() string {
	return filepath.Join(pe.ProjectDir, "supervisor", "history")
}

// Server is the ateam web server.
type Server struct {
	URL        string // set after ListenAndServe binds
	orgDir     string
	projects   []ProjectEntry
	singleMode bool
	templates  *template.Template
	md         goldmark.Markdown
}

// Close releases cached resources.
func (s *Server) Close() {
	for i := range s.projects {
		if s.projects[i].db != nil {
			s.projects[i].db.Close()
		}
	}
}

// pageData is passed to every template render.
type pageData struct {
	Title       string
	Nav         string // active nav tab
	ProjectName string // display name
	ProjectSlug string // URL-safe identifier
	Projects    []ProjectEntry
	SingleMode  bool
	Content     template.HTML
	Data        any
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"fmtCost":   display.FmtCost,
		"fmtTokens": display.FmtTokens,
		"fmtTokensInt": func(n int) string {
			return display.FmtTokens(int64(n))
		},
		"fmtDateAge": display.FmtDateAge,
		"fmtDuration": func(ms int64) string {
			if ms <= 0 {
				return ""
			}
			d := time.Duration(ms) * time.Millisecond
			if d < time.Minute {
				return fmt.Sprintf("%ds", int(d.Seconds()))
			}
			return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
		},
		"fmtTimestamp": func(s string) string {
			if s == "" {
				return ""
			}
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				return s
			}
			return t.Format("01/02 15:04")
		},
		"addInt":    func(a, b int) int { return a + b },
		"lower":     strings.ToLower,
		"estTokens": prompts.EstimateTokens,
		"fmtPercent": func(value, total int) string {
			if total <= 0 || value <= 0 {
				return ""
			}
			return fmt.Sprintf("%d%%", value*100/total)
		},
		"runsTableCtx": func(slug string, runs []overviewRun) map[string]any {
			return map[string]any{"ProjectSlug": slug, "Runs": runs}
		},
	}
}

// New creates a Server. If env has a ProjectDir, runs in single-project mode.
// Otherwise discovers projects from orgDir.
func New(env *root.ResolvedEnv) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcMap()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}

	s := &Server{
		orgDir:    env.OrgDir,
		templates: tmpl,
		md:        newMarkdown(),
	}

	if env.ProjectDir != "" {
		s.singleMode = true
		s.projects = []ProjectEntry{{
			Name:       env.ProjectName,
			Slug:       slugify(env.ProjectName),
			ProjectDir: env.ProjectDir,
			OrgDir:     env.OrgDir,
			SourceDir:  env.SourceDir,
		}}
	} else if env.OrgDir != "" {
		usedSlugs := make(map[string]bool)
		if err := root.WalkProjects(env.OrgDir, func(pi root.ProjectInfo) error {
			slug := uniqueSlug(slugify(pi.Config.Project.Name), usedSlugs)
			usedSlugs[slug] = true
			s.projects = append(s.projects, ProjectEntry{
				Name:       pi.Config.Project.Name,
				Slug:       slug,
				ProjectDir: pi.Dir,
				OrgDir:     env.OrgDir,
				SourceDir:  filepath.Dir(pi.Dir),
			})
			return nil
		}); err != nil {
			return nil, fmt.Errorf("walking projects: %w", err)
		}
	}

	return s, nil
}

func (s *Server) findProject(slug string) *ProjectEntry {
	for i := range s.projects {
		if s.projects[i].Slug == slug {
			return &s.projects[i]
		}
	}
	return nil
}

func (s *Server) getDB(pe *ProjectEntry) *calldb.CallDB {
	if pe.db != nil {
		return pe.db
	}
	dbPath := filepath.Join(pe.ProjectDir, "state.sqlite")
	db, err := calldb.OpenIfExists(dbPath)
	if err != nil {
		return nil
	}
	if db == nil {
		return nil
	}
	pe.db = db
	return db
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, tmplName string, pd pageData) {
	pd.Projects = s.projects
	pd.SingleMode = s.singleMode

	var contentBuf strings.Builder
	if err := s.templates.ExecuteTemplate(&contentBuf, tmplName, pd); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pd.Content = template.HTML(contentBuf.String())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, "layout.html", pd); err != nil {
		fmt.Fprintf(os.Stderr, "template error: %v\n", err)
	}
}

// ListenAndServe starts the HTTP server. Port 0 means a random available port.
// If openBrowser is true, opens the URL in the default browser before serving.
func (s *Server) ListenAndServe(port int, openBrowser bool, host string) error {
	mux := http.NewServeMux()

	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("embedded static files: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// Routes
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /p/{project}/", s.handleOverview)
	mux.HandleFunc("GET /p/{project}/reports", s.handleReports)
	mux.HandleFunc("GET /p/{project}/reports/{role}", s.handleReport)
	mux.Handle("GET /p/{project}/review", s.handleReview())
	mux.Handle("GET /p/{project}/verify", s.handleVerify())
	mux.HandleFunc("GET /p/{project}/prompts", s.handlePrompts)
	mux.HandleFunc("GET /p/{project}/runs", s.handleRuns)
	mux.HandleFunc("GET /p/{project}/runs/{id}", s.handleRun)
	mux.HandleFunc("GET /p/{project}/runs/{id}/{file}", s.handleRunFile)
	mux.HandleFunc("GET /p/{project}/cost", s.handleCost)
	mux.HandleFunc("GET /p/{project}/reports/{role}/history/{file}", s.handleReportHistory)
	mux.Handle("GET /p/{project}/review/history/{file}", s.handleSupervisorHistory("review"))
	mux.Handle("GET /p/{project}/verify/history/{file}", s.handleSupervisorHistory("verify"))
	mux.HandleFunc("GET /p/{project}/sessions", s.handleSessions)
	mux.HandleFunc("GET /p/{project}/sessions/{taskgroup}", s.handleSessionDetail)
	mux.HandleFunc("GET /p/{project}/code", s.handleCodeSessions)
	mux.HandleFunc("GET /p/{project}/code/{session}", s.handleCodeSessionDetail)
	mux.HandleFunc("GET /p/{project}/code/{session}/{file}", s.handleCodeSessionFile)

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	displayHost := "localhost"
	if host != "127.0.0.1" && host != "" {
		displayHost = host
	}
	s.URL = fmt.Sprintf("http://%s:%d", displayHost, actualPort)

	if s.singleMode {
		s.URL += "/p/" + s.projects[0].Slug + "/"
	}
	fmt.Fprintf(os.Stderr, "Serving at %s\n", s.URL)

	if openBrowser {
		openURL(s.URL)
	}

	srv := &http.Server{
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.Serve(ln)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func openURL(url string) {
	var cmd string
	switch goruntime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "start"
	default:
		cmd = "xdg-open"
	}
	_ = exec.Command(cmd, url).Start()
}

// uniqueSlug returns slug if unused, otherwise appends -2, -3, etc.
func uniqueSlug(slug string, used map[string]bool) string {
	if !used[slug] {
		return slug
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", slug, i)
		if !used[candidate] {
			return candidate
		}
	}
}

// slugify returns a URL-safe identifier from a project name.
func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.ReplaceAll(s, " ", "-")
	// Remove anything not alphanumeric, dash, or underscore
	var b strings.Builder
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteRune(c)
		}
	}
	if b.Len() == 0 {
		return "project"
	}
	return b.String()
}

// discoverRoles returns all known role IDs for a project.
func discoverRoles(pe *ProjectEntry) []string {
	cfg, err := config.Load(pe.ProjectDir)
	if err != nil {
		return prompts.AllRoleIDs
	}
	return prompts.AllKnownRoleIDs(cfg.Roles, pe.ProjectDir, pe.OrgDir)
}
