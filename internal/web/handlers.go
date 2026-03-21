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
		http.Redirect(w, r, "/p/"+s.projects[0].Slug+"/", http.StatusFound)
		return
	}
	s.render(w, r, "home.html", pageData{
		Title: "Projects",
		Nav:   "home",
	})
}

type overviewRun struct {
	calldb.RecentRow
	runFiles
}

type overviewData struct {
	Reports       []prompts.RoleReport
	Runs          []overviewRun
	HasReview     bool
	ReviewModTime time.Time
	CostTotal     float64
	ShowAll       bool
	TotalRuns     int
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

	showAll := r.URL.Query().Get("all") == "1"
	data.ShowAll = showAll

	if db := s.getDB(pe); db != nil {
		limit := 30
		if showAll {
			limit = 100000
		}
		rows, _ := db.RecentRuns(calldb.RecentFilter{Limit: limit})
		data.Runs = enrichRuns(rows, pe.ProjectDir)
		data.TotalRuns = len(rows)
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
		ProjectSlug: pe.Slug,
		Data:        data,
	})
}

type runFiles struct {
	ExecFile   string
	PromptFile string
	OutputFile string
	LogsDir    string
	HasStream  bool
	HasStderr  bool
}

// resolveRunFiles resolves associated files for a single run row.
func resolveRunFiles(projectDir string, row calldb.RecentRow) runFiles {
	if row.StreamFile == "" {
		return runFiles{}
	}
	var rf runFiles
	prefix := strings.TrimSuffix(row.StreamFile, "_stream.jsonl")
	execPath := filepath.Join(projectDir, prefix+"_exec.md")
	if _, err := os.Stat(execPath); err == nil {
		rf.ExecFile = filepath.Base(execPath)
	}
	if _, err := os.Stat(filepath.Join(projectDir, row.StreamFile)); err == nil {
		rf.HasStream = true
	}
	stderrPath := prefix + "_stderr.log"
	if info, err := os.Stat(filepath.Join(projectDir, stderrPath)); err == nil && info.Size() > 0 {
		rf.HasStderr = true
	}
	rf.LogsDir = filepath.Dir(row.StreamFile)
	rf.PromptFile = resolvePromptFile(projectDir, row.Action, row.Role, row.StreamFile)
	rf.OutputFile = resolveOutputFile(projectDir, row.Action, row.Role, row.StreamFile)
	return rf
}

// enrichRuns resolves associated files for each run.
// Runs are returned in descending start order (newest first).
func enrichRuns(rows []calldb.RecentRow, projectDir string) []overviewRun {
	result := make([]overviewRun, len(rows))
	for i, row := range rows {
		// rows come back ASC from calldb; reverse to DESC.
		result[len(rows)-1-i] = overviewRun{
			RecentRow: row,
			runFiles:  resolveRunFiles(projectDir, row),
		}
	}
	return result
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
		ProjectSlug: pe.Slug,
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
		ProjectSlug: pe.Slug,
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
		ProjectSlug: pe.Slug,
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
		ProjectSlug: pe.Slug,
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
		ProjectSlug: pe.Slug,
		Data:        runs,
	})
}

type runDetailData struct {
	Run calldb.RecentRow
	runFiles
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

	data := runDetailData{
		Run:      *run,
		runFiles: resolveRunFiles(pe.ProjectDir, *run),
	}

	s.render(w, r, "run.html", pageData{
		Title:       fmt.Sprintf("Run #%d", id),
		Nav:         "runs",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data:        data,
	})
}

// handleRunFile serves exec or prompt markdown files associated with a run.
func (s *Server) handleRunFile(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	if err != nil || run == nil || run.StreamFile == "" {
		http.NotFound(w, r)
		return
	}

	fileType := r.PathValue("file")
	var absPath, title string

	switch fileType {
	case "exec":
		prefix := strings.TrimSuffix(run.StreamFile, "_stream.jsonl")
		absPath = filepath.Join(pe.ProjectDir, prefix+"_exec.md")
		title = fmt.Sprintf("Run #%d — Exec", id)
	case "prompt":
		promptFile := resolvePromptFile(pe.ProjectDir, run.Action, run.Role, run.StreamFile)
		if promptFile == "" {
			http.NotFound(w, r)
			return
		}
		absPath = filepath.Join(pe.ProjectDir, promptDir(run.Action, run.Role), promptFile)
		title = fmt.Sprintf("Run #%d — Prompt", id)
	case "output":
		outputFile := resolveOutputFile(pe.ProjectDir, run.Action, run.Role, run.StreamFile)
		if outputFile == "" {
			http.NotFound(w, r)
			return
		}
		absPath = filepath.Join(pe.ProjectDir, promptDir(run.Action, run.Role), outputFile)
		title = fmt.Sprintf("Run #%d — Output", id)
	case "logs":
		absPath = filepath.Join(pe.ProjectDir, run.StreamFile)
		title = fmt.Sprintf("Run #%d — Stream Log", id)
	case "stderr":
		prefix := strings.TrimSuffix(run.StreamFile, "_stream.jsonl")
		absPath = filepath.Join(pe.ProjectDir, prefix+"_stderr.log")
		title = fmt.Sprintf("Run #%d — Stderr", id)
	default:
		http.NotFound(w, r)
		return
	}

	// Validate path stays within project dir.
	absPath = filepath.Clean(absPath)
	if !strings.HasPrefix(absPath, filepath.Clean(pe.ProjectDir)+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var rendered string
	switch fileType {
	case "logs", "stderr":
		rendered = s.renderMarkdown("```\n" + string(content) + "\n```")
	default:
		rendered = s.renderMarkdown(string(content))
	}

	s.render(w, r, "run_file.html", pageData{
		Title:       title,
		Nav:         "runs",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data: runFileData{
			RunID:    id,
			FileType: fileType,
			HTML:     template.HTML(rendered),
		},
	})
}

type runFileData struct {
	RunID    int64
	FileType string
	HTML     template.HTML
}

// promptDir returns the history directory path (relative to project dir) for an action/role.
func promptDir(action, role string) string {
	switch action {
	case runner.ActionReport, runner.ActionRun:
		return filepath.Join("roles", role, "history")
	default:
		return filepath.Join("supervisor", "history")
	}
}

// resolvePromptFile finds the archived prompt file for a run by matching timestamps.
func resolvePromptFile(projectDir, action, role, streamFile string) string {
	var promptName string
	switch action {
	case runner.ActionReport:
		promptName = "report_prompt.md"
	case runner.ActionReview:
		promptName = "review_prompt.md"
	case runner.ActionCode:
		promptName = "code_management_prompt.md"
	case runner.ActionRun:
		promptName = "run_prompt.md"
	default:
		return ""
	}
	return resolveHistoryFile(projectDir, action, role, streamFile, promptName)
}

// resolveOutputFile finds the archived output file (report.md or review.md) for a run.
func resolveOutputFile(projectDir, action, role, streamFile string) string {
	var outputName string
	switch action {
	case runner.ActionReport:
		outputName = "report.md"
	case runner.ActionReview:
		outputName = "review.md"
	default:
		return ""
	}
	return resolveHistoryFile(projectDir, action, role, streamFile, outputName)
}

// resolveHistoryFile finds an archived file by matching the stream file's timestamp.
// Exact match is expected for new runs; fuzzy ±5s fallback handles older data.
// Returns the filename (not full path), or empty if not found.
func resolveHistoryFile(projectDir, action, role, streamFile, targetName string) string {
	histDir := filepath.Join(projectDir, promptDir(action, role))

	base := filepath.Base(streamFile)
	if len(base) < 19 {
		return ""
	}
	ts := base[:19]

	exact := ts + "." + targetName
	if _, err := os.Stat(filepath.Join(histDir, exact)); err == nil {
		return exact
	}

	t, err := time.Parse(runner.TimestampFormat, ts)
	if err != nil {
		return ""
	}
	for _, offset := range []time.Duration{
		time.Second, -time.Second,
		2 * time.Second, -2 * time.Second,
		5 * time.Second, -5 * time.Second,
	} {
		candidate := t.Add(offset).Format(runner.TimestampFormat) + "." + targetName
		if _, err := os.Stat(filepath.Join(histDir, candidate)); err == nil {
			return candidate
		}
	}

	return ""
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
		ProjectSlug: pe.Slug,
		Data:        data,
	})
}

// --- History handlers ---

type historyDetailData struct {
	Kind            string // "report", "review", "code_management_prompt", etc.
	RoleID          string // empty for supervisor
	Filename        string
	Timestamp       time.Time
	HTML            template.HTML
	History         []HistoryEntry // all history entries for the timeline
	CurrentFilename string         // which entry is being viewed
}

func (s *Server) handleReportHistory(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	roleID := r.PathValue("role")
	histDir := filepath.Join(pe.ProjectDir, "roles", roleID, "history")
	s.serveHistoryFile(w, r, pe, histDir, r.PathValue("file"), roleID, "reports")
}

func (s *Server) handleReviewHistory(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	histDir := filepath.Join(pe.ProjectDir, "supervisor", "history")
	s.serveHistoryFile(w, r, pe, histDir, r.PathValue("file"), "", "review")
}

func (s *Server) serveHistoryFile(w http.ResponseWriter, r *http.Request, pe *ProjectEntry, histDir, filename, roleID, nav string) {
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
	kind := entry.Kind
	if kind == "" {
		kind = "report"
	}
	history := filterHistoryByKind(discoverHistory(histDir), kind)

	title := nav + " history"
	if roleID != "" {
		title = roleID + " history"
	}
	s.render(w, r, "history_detail.html", pageData{
		Title:       title,
		Nav:         nav,
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data: historyDetailData{
			Kind:            kind,
			RoleID:          roleID,
			Filename:        filename,
			Timestamp:       entry.Timestamp,
			HTML:            template.HTML(s.renderMarkdown(string(content))),
			History:         history,
			CurrentFilename: filename,
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
				kind := "report"
				if strings.HasPrefix(row.TaskGroup, "code-") {
					kind = "code"
				}
				cs = &CodeSession{
					TaskGroup: row.TaskGroup,
					Timestamp: ts,
					Kind:      kind,
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

	s.render(w, r, "sessions.html", pageData{
		Title:       "Sessions",
		Nav:         "sessions",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data: sessionsPageData{Sessions: sessions},
	})
}

type sessionsPageData struct {
	Sessions []CodeSession
}

func (s *Server) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	taskGroup := r.PathValue("taskgroup")
	data := sessionDetailData{TaskGroup: taskGroup}

	// Get runs from DB
	if db := s.getDB(pe); db != nil {
		data.Runs, _ = db.RecentRuns(calldb.RecentFilter{TaskGroup: taskGroup, Limit: 200})
		for _, run := range data.Runs {
			data.TotalCost += run.CostUSD
			data.TotalTokens += int64(run.InputTokens + run.OutputTokens + run.CacheReadTokens)
		}
	}

	// Collect supervisor history files matching this session's timestamp
	ts := parseTaskGroupTimestamp(taskGroup)
	tsPrefix := ts.Format(runner.TimestampFormat)

	supHistDir := filepath.Join(pe.ProjectDir, "supervisor", "history")
	for _, entry := range discoverHistory(supHistDir) {
		if entry.Timestamp.Format(runner.TimestampFormat) == tsPrefix {
			data.SupervisorFiles = append(data.SupervisorFiles, sessionFile{
				HistoryEntry: entry,
				Label:        "supervisor/" + entry.Kind,
				URL:          fmt.Sprintf("/p/%s/review/history/%s", pe.Slug, entry.Filename),
			})
		}
	}

	// Code output file
	if strings.HasPrefix(taskGroup, "code-") {
		codeOutputPath := filepath.Join(pe.ProjectDir, "supervisor", "code_output.md")
		if info, err := os.Stat(codeOutputPath); err == nil {
			content, _ := os.ReadFile(codeOutputPath)
			data.CodeOutputHTML = template.HTML(s.renderMarkdown(string(content)))
			data.CodeOutputModTime = info.ModTime()
		}
	}

	// Role history files: match via sub-run timestamps from calldb
	// Build a set of (roleID, timestamp) pairs from the runs in this task group
	runTimestamps := map[string][]string{} // roleID -> list of timestamp prefixes
	for _, run := range data.Runs {
		if run.Role == "" || run.Role == "supervisor" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, run.StartedAt); err == nil {
			runTimestamps[run.Role] = append(runTimestamps[run.Role], t.Format(runner.TimestampFormat))
		}
	}

	rolesDir := filepath.Join(pe.ProjectDir, "roles")
	for roleID, timestamps := range runTimestamps {
		roleHistDir := filepath.Join(rolesDir, roleID, "history")
		allEntries := discoverHistory(roleHistDir)
		tsSet := make(map[string]bool, len(timestamps))
		for _, ts := range timestamps {
			tsSet[ts] = true
		}
		for _, entry := range allEntries {
			if tsSet[entry.Timestamp.Format(runner.TimestampFormat)] {
				data.RoleFiles = append(data.RoleFiles, sessionFile{
					HistoryEntry: entry,
					Label:        roleID + "/" + entry.Kind,
					RoleID:       roleID,
					URL:          fmt.Sprintf("/p/%s/reports/%s/history/%s", pe.Slug, roleID, entry.Filename),
				})
			}
		}
	}

	s.render(w, r, "session_detail.html", pageData{
		Title:       taskGroup,
		Nav:         "sessions",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data:        data,
	})
}

type sessionFile struct {
	HistoryEntry
	Label  string
	RoleID string
	URL    string
}

type sessionDetailData struct {
	TaskGroup         string
	Runs              []calldb.RecentRow
	TotalCost         float64
	TotalTokens       int64
	SupervisorFiles   []sessionFile
	RoleFiles         []sessionFile
	CodeOutputHTML    template.HTML
	CodeOutputModTime time.Time
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
