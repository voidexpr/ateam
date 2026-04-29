package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportHTMLContainsProjectNameAndReport(t *testing.T) {
	projectDir := t.TempDir()

	// Seed a role report so DiscoverReports finds content.
	roleDir := filepath.Join(projectDir, "roles", "security")
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "report.md"), []byte("# Security Report\nAll clear."), 0644); err != nil {
		t.Fatal(err)
	}

	// Seed a review file.
	supervisorDir := filepath.Join(projectDir, "supervisor")
	if err := os.MkdirAll(supervisorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(supervisorDir, "review.md"), []byte("# Supervisor Review\nLooks good."), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	html, err := s.ExportHTML(ExportOptions{})
	if err != nil {
		t.Fatalf("ExportHTML: %v", err)
	}

	if !strings.Contains(html, "testproj") {
		t.Error("expected project name 'testproj' in exported HTML")
	}
	if !strings.Contains(html, "Security Report") {
		t.Error("expected report content 'Security Report' in exported HTML")
	}
}

func TestExportHTMLProjectNameOverride(t *testing.T) {
	projectDir := t.TempDir()

	// Seed a role report so the export has a non-trivial reports section.
	roleDir := filepath.Join(projectDir, "roles", "perf")
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "report.md"), []byte("# Perf Report\nFast."), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, projectDir)
	html, err := s.ExportHTML(ExportOptions{ProjectName: "custom-name"})
	if err != nil {
		t.Fatalf("ExportHTML: %v", err)
	}

	if !strings.Contains(html, "custom-name") {
		t.Error("expected overridden project name 'custom-name' in exported HTML")
	}
}

func TestExportHTMLNoProjectReturnsError(t *testing.T) {
	s := &Server{}
	_, err := s.ExportHTML(ExportOptions{})
	if err == nil {
		t.Fatal("expected error when server has no projects")
	}
}
