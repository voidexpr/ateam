package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	Stderr   string
	Duration time.Duration
	Err      error
}

// RunClaude executes "claude -p" with the prompt piped via stdin.
// Stdout is captured to outputFile and a buffer. Stderr is captured separately.
// If workDir is non-empty, the subprocess runs in that directory.
func RunClaude(ctx context.Context, prompt, outputFile, workDir string, timeoutMinutes int) RunResult {
	start := time.Now()

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	outDir := filepath.Dir(outputFile)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return RunResult{Err: fmt.Errorf("cannot create output directory: %w", err), Duration: time.Since(start)}
	}

	outFile, err := os.Create(outputFile)
	if err != nil {
		return RunResult{Err: fmt.Errorf("cannot create output file: %w", err), Duration: time.Since(start)}
	}
	defer outFile.Close()

	cmd := exec.CommandContext(ctx, "claude", "-p")
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Stdin = strings.NewReader(prompt)

	var stdoutBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(outFile, &stdoutBuf)
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	duration := time.Since(start)
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()

	// Save raw logs for every run
	writeLog(filepath.Join(outDir, "last_run_stdout.log"), stdout)
	writeLog(filepath.Join(outDir, "last_run_stderr.log"), stderr)

	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)

	if ctx.Err() == context.DeadlineExceeded {
		return RunResult{Output: stdout, Stderr: stderr, Err: fmt.Errorf("timed out after %d minutes", timeoutMinutes), Duration: duration}
	}
	if err != nil {
		return RunResult{Output: stdout, Stderr: stderr, Err: fmt.Errorf("claude exited with error: %w", err), Duration: duration}
	}

	return RunResult{Output: stdout, Duration: duration}
}

func writeLog(path, content string) {
	_ = os.WriteFile(path, []byte(content), 0644)
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

// ArchiveFile copies a file to archiveDir with a timestamped name: "2006-01-02_1504.{name}".
func ArchiveFile(srcPath, archiveDir, name string) error {
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("2006-01-02_1504")
	archiveName := strings.ReplaceAll(fmt.Sprintf("%s.%s", timestamp, name), " ", "_")

	return os.WriteFile(filepath.Join(archiveDir, archiveName), data, 0644)
}
