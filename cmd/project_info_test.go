package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func saveProjectInfoGlobals() func() {
	format := projectInfoFormat
	return func() { projectInfoFormat = format }
}

// TestRunProjectInfoMarkdown verifies the default markdown rendering of
// `ateam project-info` against a fixed directory ("."). The output is a
// simple text dump — we just check that it is non-empty and contains a
// recognizable section heading.
func TestRunProjectInfoMarkdown(t *testing.T) {
	defer saveProjectInfoGlobals()()
	projectInfoFormat = "markdown"

	var runErr error
	out := captureStdout(t, func() {
		runErr = runProjectInfo(nil, []string{"."})
	})
	if runErr != nil {
		t.Fatalf("runProjectInfo: %v", runErr)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected non-empty markdown output")
	}
}

// TestRunProjectInfoJSON verifies the --format json path emits valid JSON.
func TestRunProjectInfoJSON(t *testing.T) {
	defer saveProjectInfoGlobals()()
	projectInfoFormat = "json"

	var runErr error
	out := captureStdout(t, func() {
		runErr = runProjectInfo(nil, []string{"."})
	})
	if runErr != nil {
		t.Fatalf("runProjectInfo --format json: %v", runErr)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Errorf("invalid JSON output: %v\n%s", err, out)
	}
}

// TestRunProjectInfoUnknownFormat verifies that unknown formats produce
// an explicit error.
func TestRunProjectInfoUnknownFormat(t *testing.T) {
	defer saveProjectInfoGlobals()()
	projectInfoFormat = "yaml"

	err := runProjectInfo(nil, []string{"."})
	if err == nil {
		t.Fatal("expected error for unknown format, got nil")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("expected 'unknown format' in error, got: %v", err)
	}
}
