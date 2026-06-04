// Package web implements the web UI server and HTTP handlers for browsing project runs and reports.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/config"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/promptdata"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
	"github.com/ateam/internal/runtime"
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

	// dbOnce guards lazy CallDB initialization so concurrent HTTP handlers
	// don't open the SQLite file multiple times and overwrite each other's
	// pointer. dbErr captures the first open failure so callers can tell a
	// missing DB (nil, nil) from an open failure (nil, err).
	dbOnce sync.Once
	db     *calldb.CallDB
	dbErr  error

	// pricingOnce guards lazy loading of the runtime pricing config. The
	// runtime HCL is read once per ProjectEntry and converted into per-agent
	// PricingTable / default-model maps so HTTP handlers don't re-read it
	// from disk on every request.
	pricingOnce  sync.Once
	pricing      map[string]agent.PricingTable
	defaultModel map[string]string
}

// ProjectID returns the project identifier for scoping DB queries.
// Returns "" in org-less mode or when source/org dirs are unavailable.
func (pe *ProjectEntry) ProjectID() string {
	if pe.SourceDir == "" || pe.OrgDir == "" {
		return ""
	}
	orgRoot := filepath.Dir(pe.OrgDir)
	relPath, err := filepath.Rel(orgRoot, pe.SourceDir)
	if err != nil {
		return ""
	}
	return config.PathToProjectID(relPath)
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
	PortSource string // optional human-readable origin of the port (e.g. ".ateam/cache/serve.port"), shown in the startup banner
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
	pe.dbOnce.Do(func() {
		dbPath := filepath.Join(pe.ProjectDir, "state.sqlite")
		db, err := calldb.OpenIfExists(dbPath)
		pe.db = db
		pe.dbErr = err
	})
	return pe.db
}

// loadPricing lazily reads the project's runtime pricing config and returns
// the per-agent PricingTable and default-model maps. The maps are cached on
// the ProjectEntry so subsequent calls don't re-read the HCL from disk.
func (s *Server) loadPricing(pe *ProjectEntry) (map[string]agent.PricingTable, map[string]string) {
	pe.pricingOnce.Do(func() {
		pe.pricing = make(map[string]agent.PricingTable)
		pe.defaultModel = make(map[string]string)
		rtCfg, _ := runtime.Load(pe.ProjectDir, pe.OrgDir)
		if rtCfg == nil {
			return
		}
		for name, ac := range rtCfg.Agents {
			if ac.Pricing == nil {
				continue
			}
			table := make(agent.PricingTable, len(ac.Pricing.Models))
			for mname, mp := range ac.Pricing.Models {
				table[mname] = agent.ModelPrice{
					InputPerToken:       mp.InputPerMTok / 1e6,
					CachedInputPerToken: mp.CachedInputPerMTok / 1e6,
					OutputPerToken:      mp.OutputPerMTok / 1e6,
				}
			}
			pe.pricing[name] = table
			pe.defaultModel[name] = ac.Pricing.DefaultModel
		}
	})
	return pe.pricing, pe.defaultModel
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

// registerRoutes wires every dynamic handler onto mux. Kept separate from
// ListenAndServe so tests can build a ServeMux with the same route table the
// production server uses instead of duplicating it.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /p/{project}/", s.handleOverview)
	mux.HandleFunc("GET /p/{project}/reports", s.handleReports)
	mux.HandleFunc("GET /p/{project}/reports/{role}", s.handleReport)
	mux.Handle("GET /p/{project}/review", s.handleReview())
	mux.Handle("GET /p/{project}/verify", s.handleVerify())
	mux.HandleFunc("GET /p/{project}/prompts", s.handlePrompts)
	mux.HandleFunc("GET /p/{project}/runs", s.handleRuns)
	mux.HandleFunc("GET /p/{project}/runs/{id}", s.handleRun)
	mux.HandleFunc("GET /p/{project}/runs/{id}/runtime/{name...}", s.handleRunRuntimeFile)
	mux.HandleFunc("GET /p/{project}/runs/{id}/{file}", s.handleRunFile)
	mux.HandleFunc("GET /p/{project}/cost", s.handleCost)
	mux.HandleFunc("GET /p/{project}/reports/{role}/history/{file}", s.handleReportHistory)
	mux.Handle("GET /p/{project}/review/history/{file}", s.handleSupervisorHistory("review"))
	mux.Handle("GET /p/{project}/verify/history/{file}", s.handleSupervisorHistory("verify"))
	mux.HandleFunc("GET /p/{project}/sessions", s.handleSessions)
	mux.HandleFunc("GET /p/{project}/sessions/{batch}", s.handleSessionDetail)
	mux.HandleFunc("GET /p/{project}/code", s.handleCodeSessions)
	mux.HandleFunc("GET /p/{project}/code/{session}", s.handleCodeSessionDetail)
	mux.HandleFunc("GET /p/{project}/code/{session}/{file}", s.handleCodeSessionFile)
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

	s.registerRoutes(mux)

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
	if s.PortSource != "" {
		fmt.Fprintf(os.Stderr, "(port in %s) Serving at %s\n", s.PortSource, s.URL)
	} else {
		fmt.Fprintf(os.Stderr, "Serving at %s\n", s.URL)
	}

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

func openURL(target string) {
	if goruntime.GOOS == "darwin" && strings.HasPrefix(defaultBrowserBundleID(), "com.google.chrome") {
		if focusChromeTab(target) {
			return
		}
	}
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		// `start` is a cmd.exe builtin, not a binary, so go through rundll32
		// which avoids cmd's URL-escaping quirks.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		if b := strings.TrimSpace(os.Getenv("BROWSER")); b != "" {
			cmd = exec.Command(b, target)
		} else {
			cmd = exec.Command("xdg-open", target)
		}
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "could not launch browser (%v); open %s manually\n", err, target)
	}
}

var lsHandlerRoleRE = regexp.MustCompile(`LSHandlerRoleAll\s*=\s*"?([^";]+)"?`)

// defaultBrowserBundleID returns the macOS default HTTP handler bundle ID
// (lowercased, e.g. "com.google.chrome"). Empty on error or off-darwin.
func defaultBrowserBundleID() string {
	if goruntime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command("defaults", "read", "com.apple.LaunchServices/com.apple.launchservices.secure", "LSHandlers").Output()
	if err != nil {
		return ""
	}
	for _, block := range strings.Split(string(out), "}") {
		if !strings.Contains(block, "LSHandlerURLScheme = http;") {
			continue
		}
		if m := lsHandlerRoleRE.FindStringSubmatch(block); len(m) > 1 {
			return strings.ToLower(strings.TrimSpace(m[1]))
		}
	}
	return ""
}

// focusChromeTab asks Chrome via AppleScript to activate any existing tab
// whose URL shares target's scheme://host:port prefix, opening a new tab only
// when none matches. Returns false if osascript fails or Chrome isn't reachable
// so the caller can fall back to plain `open`.
func focusChromeTab(target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return false
	}
	prefix := u.Scheme + "://" + u.Host + "/"
	script := fmt.Sprintf(`tell application "Google Chrome"
	set matched to false
	repeat with w in windows
		set i to 1
		repeat with t in tabs of w
			if URL of t starts with %q then
				set active tab index of w to i
				set index of w to 1
				activate
				set matched to true
				exit repeat
			end if
			set i to i + 1
		end repeat
		if matched then exit repeat
	end repeat
	if not matched then
		open location %q
		activate
	end if
end tell`, prefix, target)
	return exec.Command("osascript", "-e", script).Run() == nil
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
		return promptdata.AllRoleIDs
	}
	return promptdata.AllKnownRoleIDs(cfg.Roles, pe.ProjectDir, pe.OrgDir)
}
