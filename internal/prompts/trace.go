package prompts

import (
	"os"
	"strings"
	"time"
)

// PromptSource describes a single input that contributes to a prompt.
type PromptSource struct {
	Path    string    // absolute file path, or "" for CLI/generated content
	Label   string    // display label for non-file sources (e.g. "CLI: --extra-prompt")
	ModTime time.Time // zero for non-file sources
	Content string    // raw content for token estimation
}

// DisplayPath returns a shortened path for display.
// Paths under .ateamorg/ or .ateam/ are made relative starting at that directory.
// Non-file sources return their Label.
func (s PromptSource) DisplayPath() string {
	if s.Path == "" {
		return s.Label
	}
	if i := strings.Index(s.Path, "/.ateamorg/"); i >= 0 {
		return s.Path[i+1:]
	}
	if i := strings.Index(s.Path, "/.ateam/"); i >= 0 {
		return s.Path[i+1:]
	}
	return s.Path
}

// EstimateTokens approximates token count using ~4 characters per token.
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4
}

// traceFileOr3Level tries paths in order and returns a PromptSource for the
// first existing non-empty file. Used by prompts.go's readFileOr3Level (still
// reachable from the legacy AssembleReviewPrompt / AssembleCodeManagementPrompt
// non-custom branches that the `--prompt` callers don't exercise).
func traceFileOr3Level(paths ...string) *PromptSource {
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			continue
		}
		return &PromptSource{Path: p, ModTime: info.ModTime(), Content: content}
	}
	return nil
}
