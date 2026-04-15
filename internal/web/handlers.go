package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ateam/internal/calldb"
	"github.com/ateam/internal/prompts"
	"github.com/ateam/internal/root"
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
	Reports           []prompts.RoleReport
	Runs              []overviewRun
	HasReview         bool
	ReviewModTime     time.Time
	HasCodeOutput     bool
	CodeModTime       time.Time
	LatestCodeSession string
	CostTotal         float64
	ShowAll           bool
	TotalRuns         int
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

	if latest := latestCodeSession(filepath.Join(pe.ProjectDir, "supervisor", "code")); latest != "" {
		data.LatestCodeSession = latest
		reportPath := filepath.Join(pe.ProjectDir, "supervisor", "code", latest, "execution_report.md")
		if info, err := os.Stat(reportPath); err == nil {
			data.HasCodeOutput = true
			data.CodeModTime = info.ModTime()
		}
	}

	showAll := r.URL.Query().Get("all") == "1"
	data.ShowAll = showAll

	if db := s.getDB(pe); db != nil {
		limit := 30
		if showAll {
			limit = 100000
		}
		rows, err := db.RecentRuns(calldb.RecentFilter{Limit: limit})
		if err != nil {
			log.Printf("warning: RecentRuns: %v", err)
		}
		data.Runs = enrichRuns(rows, pe.ProjectDir, pe.OrgDir)
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
	LogsDir    string
	HasStream  bool
	HasStderr  bool
}

// resolveRunFiles resolves associated files for a single run row.
func resolveRunFiles(projectDir, orgDir string, row calldb.RecentRow) runFiles {
	if row.StreamFile == "" {
		return runFiles{}
	}
	absStream := root.ResolveStreamPath(projectDir, orgDir, row.StreamFile)
	var rf runFiles
	prefix := strings.TrimSuffix(absStream, "_stream.jsonl")
	if _, err := os.Stat(prefix + "_exec.md"); err == nil {
		rf.ExecFile = filepath.Base(prefix + "_exec.md")
	}
	if _, err := os.Stat(absStream); err == nil {
		rf.HasStream = true
	}
	if info, err := os.Stat(prefix + "_stderr.log"); err == nil && info.Size() > 0 {
		rf.HasStderr = true
	}
	rf.LogsDir = filepath.Dir(row.StreamFile)
	rf.PromptFile = resolvePromptFile(projectDir, row.Action, row.Role, row.StreamFile)
	return rf
}

// enrichRuns resolves associated files for each run.
// Rows are expected in descending start order (newest first) from calldb.
func enrichRuns(rows []calldb.RecentRow, projectDir, orgDir string) []overviewRun {
	result := make([]overviewRun, len(rows))
	for i, row := range rows {
		result[i] = overviewRun{
			RecentRow: row,
			runFiles:  resolveRunFiles(projectDir, orgDir, row),
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
	RoleID             string
	ModTime            time.Time
	HTML               template.HTML
	History            []HistoryEntry // past report.md versions
	CurrentCostUSD     float64
	CurrentTotalTokens int64
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
			costs := fetchRunCosts(s.getDB(pe), runner.ActionReport, roleID)
			enrichHistoryCost(history, costs)
			curCost, curTokens := latestRunCost(costs)
			s.render(w, r, "report.html", pageData{
				Title:       roleID + " report",
				Nav:         "reports",
				ProjectName: pe.Name,
				ProjectSlug: pe.Slug,
				Data: reportData{
					RoleID:             roleID,
					ModTime:            rpt.ModTime,
					HTML:               template.HTML(s.renderMarkdown(rpt.Content)),
					History:            history,
					CurrentCostUSD:     curCost,
					CurrentTotalTokens: curTokens,
				},
			})
			return
		}
	}
	http.NotFound(w, r)
}

type reviewData struct {
	HTML               template.HTML
	ModTime            time.Time
	Exists             bool
	History            []HistoryEntry // past review.md versions
	CurrentCostUSD     float64
	CurrentTotalTokens int64
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	reviewPath := filepath.Join(pe.ProjectDir, "supervisor", "review.md")
	data := reviewData{}
	if content, modTime, err := readFileWithModTime(reviewPath); err == nil {
		data.Exists = true
		data.HTML = template.HTML(s.renderMarkdown(string(content)))
		data.ModTime = modTime
	}
	histDir := filepath.Join(pe.ProjectDir, "supervisor", "history")
	data.History = filterHistoryByKind(discoverHistory(histDir), "review")
	costs := fetchRunCosts(s.getDB(pe), runner.ActionReview, "supervisor")
	enrichHistoryCost(data.History, costs)
	data.CurrentCostUSD, data.CurrentTotalTokens = latestRunCost(costs)

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

type runsPageData struct {
	Runs      []overviewRun
	TotalRuns int
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	data := runsPageData{}
	if db := s.getDB(pe); db != nil {
		rows, err := db.RecentRuns(calldb.RecentFilter{Limit: -1})
		if err != nil {
			log.Printf("warning: RecentRuns: %v", err)
		}
		data.Runs = enrichRuns(rows, pe.ProjectDir, pe.OrgDir)
		data.TotalRuns = len(rows)
	}

	s.render(w, r, "runs.html", pageData{
		Title:       "Runs",
		Nav:         "runs",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data:        data,
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
		runFiles: resolveRunFiles(pe.ProjectDir, pe.OrgDir, *run),
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
	absStream := root.ResolveStreamPath(pe.ProjectDir, pe.OrgDir, run.StreamFile)
	var absPath, title string

	switch fileType {
	case "exec":
		prefix := strings.TrimSuffix(absStream, "_stream.jsonl")
		absPath = prefix + "_exec.md"
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
		absPath = absStream
		title = fmt.Sprintf("Run #%d — Stream Log", id)
	case "stderr":
		prefix := strings.TrimSuffix(absStream, "_stream.jsonl")
		absPath = prefix + "_stderr.log"
		title = fmt.Sprintf("Run #%d — Stderr", id)
	default:
		http.NotFound(w, r)
		return
	}

	absPath = filepath.Clean(absPath)
	if !isPathWithin(absPath, pe.ProjectDir) && !isPathWithin(absPath, pe.OrgDir) {
		http.NotFound(w, r)
		return
	}

	var rendered string
	if fileType == "logs" {
		var buf strings.Builder
		f := &runner.HTMLStreamFormatter{}
		if err := f.FormatFile(absPath, &buf); err != nil {
			http.NotFound(w, r)
			return
		}
		rendered = buf.String()
	} else {
		content, err := os.ReadFile(absPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch fileType {
		case "stderr":
			rendered = s.renderMarkdown("```\n" + string(content) + "\n```")
		default:
			rendered = s.renderMarkdown(string(content))
		}
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

// readFileWithModTime reads a file and returns its content and modification time.
func readFileWithModTime(path string) ([]byte, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, err
	}
	return content, info.ModTime(), nil
}

// isPathWithin checks that absPath is under baseDir after cleaning.
func isPathWithin(absPath, baseDir string) bool {
	if baseDir == "" {
		return false
	}
	return strings.HasPrefix(filepath.Clean(absPath), filepath.Clean(baseDir)+string(filepath.Separator))
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
	Sessions    []CodeSession
	TotalCost   float64
	TotalTokens int64
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
		var err error
		data.Actions, err = db.CostByAction("")
		if err != nil {
			log.Printf("warning: CostByAction: %v", err)
		}
		for _, a := range data.Actions {
			data.TotalCost += a.CostUSD
			data.TotalTokens += a.TotalTokens
		}
		data.Sessions = buildSessions(db)
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
	if !isPathWithin(path, histDir) {
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

	action := runner.ActionReview
	role := "supervisor"
	if roleID != "" {
		action = runner.ActionReport
		role = roleID
	}
	enrichHistoryCost(history, fetchRunCosts(s.getDB(pe), action, role))

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

// buildSessions aggregates CostByTaskGroup rows into CodeSession entries.
func buildSessions(db *calldb.CallDB) []CodeSession {
	tgRows, err := db.CostByTaskGroup("")
	if err != nil {
		log.Printf("warning: CostByTaskGroup: %v", err)
	}
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
	var sessions []CodeSession
	for _, name := range order {
		sessions = append(sessions, *seen[name])
	}
	return sessions
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	var sessions []CodeSession
	if db := s.getDB(pe); db != nil {
		sessions = buildSessions(db)
	}

	s.render(w, r, "sessions.html", pageData{
		Title:       "Sessions",
		Nav:         "sessions",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data:        sessionsPageData{Sessions: sessions},
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

	if db := s.getDB(pe); db != nil {
		var err error
		data.Runs, err = db.RecentRuns(calldb.RecentFilter{TaskGroup: taskGroup, Limit: 200})
		if err != nil {
			log.Printf("warning: RecentRuns: %v", err)
		}
		for _, run := range data.Runs {
			data.TotalCost += run.CostUSD
			data.TotalTokens += int64(run.InputTokens + run.OutputTokens + run.CacheReadTokens + run.CacheWriteTokens)
		}
	}

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

	if strings.HasPrefix(taskGroup, "code-") {
		codeOutputPath := filepath.Join(pe.ProjectDir, "supervisor", "code_output.md")
		if content, modTime, err := readFileWithModTime(codeOutputPath); err == nil {
			data.CodeOutputHTML = template.HTML(s.renderMarkdown(string(content)))
			data.CodeOutputModTime = modTime
		}
	}

	runTimestamps := map[string][]string{}
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

// codeSessionEntry represents a single timestamped code session directory.
type codeSessionEntry struct {
	DirName   string
	Timestamp time.Time
	TaskCount int
	HasReport bool
}

type codeSessionsData struct {
	Sessions []codeSessionEntry
}

func (s *Server) handleCodeSessions(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	codeDir := filepath.Join(pe.ProjectDir, "supervisor", "code")
	sessions := scanCodeSessions(codeDir)

	s.render(w, r, "code_sessions.html", pageData{
		Title:       "Code",
		Nav:         "code",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data:        codeSessionsData{Sessions: sessions},
	})
}

// scanCodeSessions reads the code directory and returns sessions sorted newest-first.
func scanCodeSessions(codeDir string) []codeSessionEntry {
	entries, err := os.ReadDir(codeDir)
	if err != nil {
		return nil
	}

	var sessions []codeSessionEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ts, err := time.ParseInLocation(runner.TimestampFormat, e.Name(), time.Local)
		if err != nil {
			continue
		}
		sessionDir := filepath.Join(codeDir, e.Name())
		subEntries, _ := os.ReadDir(sessionDir)
		var taskCount int
		var hasReport bool
		for _, se := range subEntries {
			if se.IsDir() {
				continue
			}
			if strings.HasSuffix(se.Name(), "_code_prompt.md") {
				taskCount++
			}
			if se.Name() == "execution_report.md" {
				hasReport = true
			}
		}
		sessions = append(sessions, codeSessionEntry{
			DirName:   e.Name(),
			Timestamp: ts,
			TaskCount: taskCount,
			HasReport: hasReport,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Timestamp.After(sessions[j].Timestamp)
	})
	return sessions
}

// latestCodeSession returns the directory name of the newest code session, or "".
func latestCodeSession(codeDir string) string {
	sessions := scanCodeSessions(codeDir)
	if len(sessions) == 0 {
		return ""
	}
	return sessions[0].DirName
}

type codeSessionFile struct {
	Name    string
	Size    int64
	ModTime time.Time
}

type codeSessionDetailData struct {
	DirName   string
	Timestamp time.Time
	Files     []codeSessionFile
	HasReport bool
}

func (s *Server) handleCodeSessionDetail(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	dirName := r.PathValue("session")
	ts, err := time.ParseInLocation(runner.TimestampFormat, dirName, time.Local)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	sessionDir := filepath.Join(pe.ProjectDir, "supervisor", "code", dirName)
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	data := codeSessionDetailData{
		DirName:   dirName,
		Timestamp: ts,
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "current_task.md" {
			continue
		}
		info, _ := e.Info()
		var size int64
		var modTime time.Time
		if info != nil {
			size = info.Size()
			modTime = info.ModTime()
		}
		if e.Name() == "execution_report.md" {
			data.HasReport = true
		}
		data.Files = append(data.Files, codeSessionFile{
			Name:    e.Name(),
			Size:    size,
			ModTime: modTime,
		})
	}

	s.render(w, r, "code_session_detail.html", pageData{
		Title:       dirName,
		Nav:         "code",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data:        data,
	})
}

func (s *Server) handleCodeSessionFile(w http.ResponseWriter, r *http.Request) {
	pe := s.findProject(r.PathValue("project"))
	if pe == nil {
		http.NotFound(w, r)
		return
	}

	dirName := r.PathValue("session")
	if _, err := time.ParseInLocation(runner.TimestampFormat, dirName, time.Local); err != nil {
		http.NotFound(w, r)
		return
	}

	fileName := r.PathValue("file")
	if !strings.HasSuffix(fileName, ".md") || strings.Contains(fileName, "/") || strings.Contains(fileName, "..") {
		http.NotFound(w, r)
		return
	}

	absPath := filepath.Clean(filepath.Join(pe.ProjectDir, "supervisor", "code", dirName, fileName))
	if !isPathWithin(absPath, pe.ProjectDir) {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	s.render(w, r, "code_session_file.html", pageData{
		Title:       fileName,
		Nav:         "code",
		ProjectName: pe.Name,
		ProjectSlug: pe.Slug,
		Data: codeSessionFileData{
			DirName:  dirName,
			FileName: fileName,
			HTML:     template.HTML(s.renderMarkdown(string(content))),
		},
	})
}

type codeSessionFileData struct {
	DirName  string
	FileName string
	HTML     template.HTML
}
