package web

import (
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/runner"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.singleMode {
		http.Redirect(w, r, "/p/"+s.projects[0].Name+"/", http.StatusFound)
		return
	}
	s.render(w, r, "home.html", pageData{
		Title: "Projects",
		Nav:   "home",
	})
}

type overviewData struct {
	Reports       []prompts.RoleReport
	RecentRuns    []calldb.RecentRow
	HasReview     bool
	ReviewModTime time.Time
	CostTotal     float64
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	data := overviewData{}
	data.Reports, _ = prompts.DiscoverReports(pe.ProjectDir)

	reviewPath := filepath.Join(pe.ProjectDir, "supervisor", "review.md")
	if info, err := os.Stat(reviewPath); err == nil {
		data.HasReview = true
		data.ReviewModTime = info.ModTime()
	}

	if db := s.getDB(pe); db != nil {
		data.RecentRuns, _ = db.RecentRuns(calldb.RecentFilter{Limit: 10})
		if aggs, err := db.CostByAction(""); err == nil {
			for _, a := range aggs {
				data.CostTotal += a.CostUSD
			}
		}
	}

	s.render(w, r, "overview.html", pageData{
		Title:       pe.Name,
		Nav:         "overview",
		ProjectName: pe.Name,
		Data:        data,
	})
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	reports, _ := prompts.DiscoverReports(pe.ProjectDir)

	s.render(w, r, "reports.html", pageData{
		Title:       "Reports",
		Nav:         "reports",
		ProjectName: pe.Name,
		Data:        reports,
	})
}

type reportData struct {
	RoleID  string
	ModTime time.Time
	HTML    template.HTML
	History []HistoryEntry // past report.md versions
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	roleID := r.PathValue("role")
	reports, _ := prompts.DiscoverReports(pe.ProjectDir)
	for _, rpt := range reports {
		if rpt.RoleID == roleID {
			histDir := filepath.Join(pe.ProjectDir, "roles", roleID, "history")
			history := filterHistoryByKind(discoverHistory(histDir), "report")
			s.render(w, r, "report.html", pageData{
				Title:       roleID + " report",
				Nav:         "reports",
				ProjectName: pe.Name,
				Data: reportData{
					RoleID:  roleID,
					ModTime: rpt.ModTime,
					HTML:    template.HTML(s.renderMarkdown(rpt.Content)),
					History: history,
				},
			})
			return
		}
	}
	http.NotFound(w, r)
}

type reviewData struct {
	HTML    template.HTML
	ModTime time.Time
	Exists  bool
	History []HistoryEntry // past review.md versions
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	reviewPath := pe.ProjectDir + "/supervisor/review.md"
	data := reviewData{}
	if content, err := os.ReadFile(reviewPath); err == nil {
		data.Exists = true
		data.HTML = template.HTML(s.renderMarkdown(string(content)))
		if info, err := os.Stat(reviewPath); err == nil {
			data.ModTime = info.ModTime()
		}
	}
	histDir := filepath.Join(pe.ProjectDir, "supervisor", "history")
	data.History = filterHistoryByKind(discoverHistory(histDir), "review")

	s.render(w, r, "review.html", pageData{
		Title:       "Review",
		Nav:         "review",
		ProjectName: pe.Name,
		Data:        data,
	})
}

type promptsPageData struct {
	Roles []promptRoleEntry
}

type promptRoleEntry struct {
	RoleID        string
	ReportSources []prompts.PromptSource
	CodeSources   []prompts.PromptSource
	ReportTokens  int
	CodeTokens    int
}

func (s *Server) handlePrompts(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	roles := discoverRoles(pe)
	var pinfo prompts.ProjectInfoParams // empty, no CLI context

	data := promptsPageData{}
	for _, roleID := range roles {
		entry := promptRoleEntry{RoleID: roleID}
		entry.ReportSources = prompts.TraceRolePromptSources(pe.OrgDir, pe.ProjectDir, roleID, pe.SourceDir, "", pinfo, true)
		entry.CodeSources = prompts.TraceRoleCodePromptSources(pe.OrgDir, pe.ProjectDir, roleID, pe.SourceDir, "", pinfo)
		for _, src := range entry.ReportSources {
			entry.ReportTokens += prompts.EstimateTokens(src.Content)
		}
		for _, src := range entry.CodeSources {
			entry.CodeTokens += prompts.EstimateTokens(src.Content)
		}
		data.Roles = append(data.Roles, entry)
	}

	s.render(w, r, "prompts.html", pageData{
		Title:       "Prompts",
		Nav:         "prompts",
		ProjectName: pe.Name,
		Data:        data,
	})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	var runs []calldb.RecentRow
	if db := s.getDB(pe); db != nil {
		runs, _ = db.RecentRuns(calldb.RecentFilter{Limit: 100})
	}

	s.render(w, r, "runs.html", pageData{
		Title:       "Runs",
		Nav:         "runs",
		ProjectName: pe.Name,
		Data:        runs,
	})
}

type runDetailData struct {
	Run calldb.RecentRow
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	db := s.getDB(pe)
	if db == nil {
		http.NotFound(w, r)
		return
	}

	run, err := db.GetRunByID(id)
	if err != nil || run == nil {
		http.NotFound(w, r)
		return
	}

	s.render(w, r, "run.html", pageData{
		Title:       fmt.Sprintf("Run #%d", id),
		Nav:         "runs",
		ProjectName: pe.Name,
		Data:        runDetailData{Run: *run},
	})
}

type costPageData struct {
	Actions     []calldb.ActionAgg
	TaskGroups  []taskGroupSummary
	TotalCost   float64
	TotalTokens int64
}

type taskGroupSummary struct {
	Name         string
	Rows         []calldb.TaskGroupRow
	TotalCost    float64
	TotalTokens  int64
	FirstStarted string
	LastEnded    string
}

func (s *Server) handleCost(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	data := costPageData{}
	db := s.getDB(pe)
	if db != nil {
		data.Actions, _ = db.CostByAction("")
		for _, a := range data.Actions {
			data.TotalCost += a.CostUSD
			data.TotalTokens += a.TotalTokens
		}

		tgRows, _ := db.CostByTaskGroup("")
		groups := map[string]*taskGroupSummary{}
		var order []string
		for _, row := range tgRows {
			g, ok := groups[row.TaskGroup]
			if !ok {
				g = &taskGroupSummary{Name: row.TaskGroup}
				groups[row.TaskGroup] = g
				order = append(order, row.TaskGroup)
			}
			g.Rows = append(g.Rows, row)
			g.TotalCost += row.CostUSD
			g.TotalTokens += row.TotalTokens
			if g.FirstStarted == "" || row.FirstStarted < g.FirstStarted {
				g.FirstStarted = row.FirstStarted
			}
			if row.LastEnded.Valid && row.LastEnded.String > g.LastEnded {
				g.LastEnded = row.LastEnded.String
			}
		}
		for _, name := range order {
			data.TaskGroups = append(data.TaskGroups, *groups[name])
		}
	}

	s.render(w, r, "cost.html", pageData{
		Title:       "Cost",
		Nav:         "cost",
		ProjectName: pe.Name,
		Data:        data,
	})
}

// --- History handlers ---

type historyDetailData struct {
	Kind      string // "report", "review", "code_management_prompt", etc.
	RoleID    string // empty for supervisor
	Filename  string
	Timestamp time.Time
	HTML      template.HTML
}

func (s *Server) handleReportHistory(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	roleID := r.PathValue("role")
	histDir := filepath.Join(pe.ProjectDir, "roles", roleID, "history")
	s.serveHistoryFile(w, r, pe.Name, histDir, r.PathValue("file"), roleID, "reports")
}

func (s *Server) handleReviewHistory(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	histDir := filepath.Join(pe.ProjectDir, "supervisor", "history")
	s.serveHistoryFile(w, r, pe.Name, histDir, r.PathValue("file"), "", "review")
}

func (s *Server) serveHistoryFile(w http.ResponseWriter, r *http.Request, projectName, histDir, filename, roleID, nav string) {
	path := filepath.Clean(filepath.Join(histDir, filename))
	if !strings.HasPrefix(path, filepath.Clean(histDir)+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	entry := parseHistoryFilename(filename, path)
	title := nav + " history"
	if roleID != "" {
		title = roleID + " history"
	}
	s.render(w, r, "history_detail.html", pageData{
		Title:       title,
		Nav:         nav,
		ProjectName: projectName,
		Data: historyDetailData{
			Kind:      entry.Kind,
			RoleID:    roleID,
			Filename:  filename,
			Timestamp: entry.Timestamp,
			HTML:      template.HTML(s.renderMarkdown(string(content))),
		},
	})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	var sessions []CodeSession
	db := s.getDB(pe)
	if db != nil {
		tgRows, _ := db.CostByTaskGroup("")
		seen := map[string]*CodeSession{}
		var order []string
		for _, row := range tgRows {
			cs, ok := seen[row.TaskGroup]
			if !ok {
				ts := parseTaskGroupTimestamp(row.TaskGroup)
				cs = &CodeSession{
					TaskGroup: row.TaskGroup,
					Timestamp: ts,
					Label:     row.TaskGroup,
				}
				seen[row.TaskGroup] = cs
				order = append(order, row.TaskGroup)
			}
			cs.RunCount += row.Count
			cs.TotalCost += row.CostUSD
			cs.Tokens += row.TotalTokens
		}
		for _, name := range order {
			sessions = append(sessions, *seen[name])
		}
	}

	// Also collect supervisor history (code_management_prompt entries)
	supHistDir := filepath.Join(pe.ProjectDir, "supervisor", "history")
	codePrompts := filterHistoryByKind(discoverHistory(supHistDir), "code_management_prompt")

	s.render(w, r, "sessions.html", pageData{
		Title:       "Sessions",
		Nav:         "sessions",
		ProjectName: pe.Name,
		Data: sessionsPageData{
			Sessions:    sessions,
			CodePrompts: codePrompts,
		},
	})
}

type sessionsPageData struct {
	Sessions    []CodeSession
	CodePrompts []HistoryEntry
}

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	taskGroup := r.PathValue("taskgroup")
	db := s.getDB(pe)
	if db == nil {
		http.NotFound(w, r)
		return
	}

	runs, _ := db.RecentRuns(calldb.RecentFilter{TaskGroup: taskGroup, Limit: 200})

	var totalCost float64
	var totalTokens int64
	for _, run := range runs {
		totalCost += run.CostUSD
		totalTokens += int64(run.InputTokens + run.OutputTokens + run.CacheReadTokens)
	}

	s.render(w, r, "session_detail.html", pageData{
		Title:       taskGroup,
		Nav:         "sessions",
		ProjectName: pe.Name,
		Data: sessionDetailData{
			TaskGroup:   taskGroup,
			Runs:        runs,
			TotalCost:   totalCost,
			TotalTokens: totalTokens,
		},
	})
}

type sessionDetailData struct {
	TaskGroup   string
	Runs        []calldb.RecentRow
	TotalCost   float64
	TotalTokens int64
}

// parseTaskGroupTimestamp extracts timestamp from "code-2026-03-19_00-35-57" or "report-2026-03-19_00-35-57".
func parseTaskGroupTimestamp(tg string) time.Time {
	idx := strings.IndexByte(tg, '-')
	if idx < 0 || idx+1 >= len(tg) {
		return time.Time{}
	}
	tsStr := tg[idx+1:]
	t, err := time.ParseInLocation(runner.TimestampFormat, tsStr, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}
