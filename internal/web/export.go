package web

import (
	"fmt"
	"html/template"
	"path/filepath"
	"strings"
	"time"

	"github.com/ateam/internal/prompts"
)

// ExportOptions configures the HTML export.
type ExportOptions struct {
	ProjectName string // override display name (empty = use project config name)
}

type exportData struct {
	ProjectName string
	GeneratedAt time.Time
	SourcePath  string
	CSS         template.CSS
	Reports     []exportReport
	ReviewHTML  template.HTML
	ReviewAge   time.Time
	HasReview   bool
	CodeHTML    template.HTML
	CodeSession string
	CodeAge     time.Time
	HasCode     bool
	VerifyHTML  template.HTML
	VerifyAge   time.Time
	HasVerify   bool
}

type exportReport struct {
	RoleID  string
	ModTime time.Time
	HTML    template.HTML
}

// ExportHTML renders a self-contained HTML page with reports, review, and code execution report.
func (s *Server) ExportHTML(opts ExportOptions) (string, error) {
	if len(s.projects) == 0 {
		return "", fmt.Errorf("no project found")
	}
	pe := &s.projects[0]

	cssBytes, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		return "", fmt.Errorf("reading embedded CSS: %w", err)
	}

	name := pe.Name
	if opts.ProjectName != "" {
		name = opts.ProjectName
	}

	data := exportData{
		ProjectName: name,
		GeneratedAt: time.Now(),
		SourcePath:  pe.SourceDir,
		CSS:         template.CSS(cssBytes),
	}

	reports, _ := prompts.DiscoverReports(pe.ProjectDir)
	for _, rpt := range reports {
		data.Reports = append(data.Reports, exportReport{
			RoleID:  rpt.RoleID,
			ModTime: rpt.ModTime,
			HTML:    template.HTML(s.renderMarkdown(rpt.Content)),
		})
	}

	reviewPath := filepath.Join(pe.ProjectDir, "supervisor", "review.md")
	if content, modTime, err := readFileWithModTime(reviewPath); err == nil {
		data.HasReview = true
		data.ReviewHTML = template.HTML(s.renderMarkdown(string(content)))
		data.ReviewAge = modTime
	}

	codeDir := filepath.Join(pe.ProjectDir, "supervisor", "code")
	if latest := latestCodeSession(codeDir); latest != "" {
		reportPath := filepath.Join(codeDir, latest, "execution_report.md")
		if content, modTime, err := readFileWithModTime(reportPath); err == nil {
			data.HasCode = true
			data.CodeSession = latest
			data.CodeHTML = template.HTML(s.renderMarkdown(string(content)))
			data.CodeAge = modTime
		}
	}

	verifyPath := filepath.Join(pe.ProjectDir, "supervisor", "verify.md")
	if content, modTime, err := readFileWithModTime(verifyPath); err == nil {
		data.HasVerify = true
		data.VerifyHTML = template.HTML(s.renderMarkdown(string(content)))
		data.VerifyAge = modTime
	}

	var buf strings.Builder
	if err := s.templates.ExecuteTemplate(&buf, "export.html", data); err != nil {
		return "", fmt.Errorf("rendering export template: %w", err)
	}
	return buf.String(), nil
}
