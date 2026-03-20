package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
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
	Name       string
	ProjectDir string
	OrgDir     string
	SourceDir  string
	db         *calldb.CallDB // cached; may be nil
}

// Server is the ateam web server.
type Server struct {
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
	ProjectName string
	Projects    []ProjectEntry
	SingleMode  bool
	Content     template.HTML
	Data        any
}

func fmtTokensI64(n int64) string {
	if n <= 0 {
		return ""
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"fmtCost": func(c float64) string {
			if c <= 0 {
				return ""
			}
			return fmt.Sprintf("$%.2f", c)
		},
		"fmtTokens": fmtTokensI64,
		"fmtTokensInt": func(n int) string {
			return fmtTokensI64(int64(n))
		},
		"fmtDateAge": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			date := t.Format("01/02")
			age := time.Since(t)
			switch {
			case age < time.Minute:
				return date + " (just now)"
			case age < time.Hour:
				return fmt.Sprintf("%s (%dm ago)", date, int(age.Minutes()))
			case age < 24*time.Hour:
				return fmt.Sprintf("%s (%dh ago)", date, int(age.Hours()))
			default:
				days := int(age.Hours()) / 24
				return fmt.Sprintf("%s (%dd ago)", date, days)
			}
		},
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
		"lower":     strings.ToLower,
		"estTokens": prompts.EstimateTokens,
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
			ProjectDir: env.ProjectDir,
			OrgDir:     env.OrgDir,
			SourceDir:  env.SourceDir,
		}}
	} else if env.OrgDir != "" {
		if err := root.WalkProjects(env.OrgDir, func(pi root.ProjectInfo) error {
			s.projects = append(s.projects, ProjectEntry{
				Name:       pi.Config.Project.Name,
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

func (s *Server) findProject(name string) *ProjectEntry {
	for i := range s.projects {
		if s.projects[i].Name == name {
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
	db, err := calldb.Open(dbPath)
	if err != nil {
		return nil
	}
	pe.db = db
	return db
}

func (s *Server) loadConfig(pe *ProjectEntry) *config.Config {
	cfg, err := config.Load(pe.ProjectDir)
	if err != nil {
		return nil
	}
	return cfg
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
func (s *Server) ListenAndServe(port int) error {
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
	mux.HandleFunc("GET /p/{project}/review", s.handleReview)
	mux.HandleFunc("GET /p/{project}/prompts", s.handlePrompts)
	mux.HandleFunc("GET /p/{project}/runs", s.handleRuns)
	mux.HandleFunc("GET /p/{project}/runs/{id}", s.handleRun)
	mux.HandleFunc("GET /p/{project}/cost", s.handleCost)
	mux.HandleFunc("GET /p/{project}/reports/{role}/history/{file}", s.handleReportHistory)
	mux.HandleFunc("GET /p/{project}/review/history/{file}", s.handleReviewHistory)
	mux.HandleFunc("GET /p/{project}/sessions", s.handleSessions)
	mux.HandleFunc("GET /p/{project}/sessions/{taskgroup}", s.handleSessionDetail)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d", actualPort)

	if s.singleMode {
		url += "/p/" + s.projects[0].Name + "/"
	}
	fmt.Fprintf(os.Stderr, "Serving at %s\n", url)

	return http.Serve(ln, mux)
}

// discoverRoles returns all known role IDs for a project.
func discoverRoles(pe *ProjectEntry) []string {
	cfg, err := config.Load(pe.ProjectDir)
	if err != nil {
		return prompts.AllRoleIDs
	}
	return prompts.AllKnownRoleIDs(cfg.Roles)
}
