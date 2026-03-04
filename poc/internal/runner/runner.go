package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunResult holds the outcome of a single claude invocation.
type RunResult struct {
	AgentID  string
	Output   string
	Duration time.Duration
	Err      error
}

// RunClaude executes "claude -p PROMPT > outputFile" with a timeout.
// The prompt is passed via stdin to avoid shell escaping issues with large prompts.
func RunClaude(ctx context.Context, prompt, outputFile string, timeoutMinutes int) RunResult {
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputFile), 0755); err != nil {
		return RunResult{Err: fmt.Errorf("cannot create output directory: %w", err), Duration: time.Since(start)}
	}

	outFile, err := os.Create(outputFile)
	if err != nil {
		return RunResult{Err: fmt.Errorf("cannot create output file: %w", err), Duration: time.Since(start)}
	}
	defer outFile.Close()

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
	cmd.Stdout = outFile
	cmd.Stderr = os.Stderr

	err = cmd.Run()
	duration := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return RunResult{Err: fmt.Errorf("timed out after %d minutes", timeoutMinutes), Duration: duration}
	}
	if err != nil {
		return RunResult{Err: fmt.Errorf("claude exited with error: %w", err), Duration: duration}
	}

	// Read back output for the result
	data, _ := os.ReadFile(outputFile)
	return RunResult{Output: string(data), Duration: duration}
}

// FormatDuration returns a human-readable duration string.
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	if seconds == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

// ArchiveFile copies a report file to the archive directory with a timestamped name.
func ArchiveFile(srcPath, archiveDir, prefix string) error {
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("2006-01-02_1504")
	archiveName := fmt.Sprintf("%s.%s", timestamp, filepath.Base(srcPath))
	if prefix != "" {
		archiveName = fmt.Sprintf("%s.%s", timestamp, prefix)
	}

	// Remove characters that are problematic in filenames
	archiveName = strings.ReplaceAll(archiveName, " ", "_")

	return os.WriteFile(filepath.Join(archiveDir, archiveName), data, 0644)
}
