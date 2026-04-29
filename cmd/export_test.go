package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportAteamProjectFlag(t *testing.T) {
	ateamDir := t.TempDir()

	// Seed a role report so ExportHTML has content to render.
	roleDir := filepath.Join(ateamDir, "roles", "security")
	if err := os.MkdirAll(roleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roleDir, "report.md"), []byte("# Security Report\nAll clear."), 0644); err != nil {
		t.Fatal(err)
	}

	outputFile := filepath.Join(t.TempDir(), "out.html")

	// Save and restore package-level export globals.
	savedAteamProject := exportAteamProject
	savedOutput := exportOutput
	savedProjectName := exportProjectName
	defer func() {
		exportAteamProject = savedAteamProject
		exportOutput = savedOutput
		exportProjectName = savedProjectName
	}()

	exportAteamProject = ateamDir
	exportOutput = outputFile
	exportProjectName = ""

	var runErr error
	out := captureStdout(t, func() {
		runErr = runExport(nil, nil)
	})

	if runErr != nil {
		t.Fatalf("runExport: %v", runErr)
	}

	// stdout should contain the absolute path to the output file.
	if !strings.Contains(out, outputFile) {
		t.Errorf("expected output path in stdout, got: %q", out)
	}

	// The output file should exist and contain HTML.
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), "<html") {
		t.Error("expected output file to contain HTML")
	}
}
