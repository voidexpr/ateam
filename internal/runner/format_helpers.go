package runner

import (
	"fmt"
	"strings"
	"time"

	"github.com/ateam/internal/agent"
	"github.com/ateam/internal/display"
	"github.com/ateam/internal/streamutil"
)

// apiErrorPrefixes are best-effort markers for claude-CLI synthesized error
// messages. Heuristic only; if claude renames its prefixes this list goes stale.
var apiErrorPrefixes = []string{
	"API Error:",
	"API error:",
	"Error:",
	"Stream idle timeout",
	"Request timed out",
	"Credit balance is too low",
}

// looksLikeAPIError reports whether a text-block message reads like a
// claude-CLI synthesized error rather than a real assistant response.
func looksLikeAPIError(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, p := range apiErrorPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// toolResultSize returns the byte length and line count of a tool_result
// content string. A trailing newline counts as a delimiter, not a separate line.
func toolResultSize(content string) (bytes, lines int) {
	bytes = len(content)
	if bytes == 0 {
		return 0, 0
	}
	lines = strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		lines++
	}
	return
}

// contextEstimate returns the per-message context size (input + cache_read +
// cache_create) and the model's context window in tokens. window is 0 when
// the model is unknown — callers should drop the "%" display in that case.
func contextEstimate(u *streamutil.AssistantUsage, model string) (tokens, window int) {
	if u == nil {
		return 0, 0
	}
	tokens = u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	window = agent.ContextWindow(model)
	return
}

// usageParts renders a per-message usage line as a slice of "key=value"
// chunks. Each formatter (text/HTML) wraps the joined output with its own
// styling. Returns nil when usage is nil or wholly zero (e.g. claude's
// synthetic error message).
func usageParts(u *streamutil.AssistantUsage, model string) []string {
	if u == nil {
		return nil
	}
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0 {
		return nil
	}
	var parts []string
	if u.InputTokens > 0 {
		parts = append(parts, fmt.Sprintf("in=%s", display.FmtTokens(int64(u.InputTokens))))
	}
	if u.OutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("out=%s", display.FmtTokens(int64(u.OutputTokens))))
	}
	if u.CacheReadInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("cache_read=%s", display.FmtTokens(int64(u.CacheReadInputTokens))))
	}
	if u.CacheCreationInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("cache_create=%s", display.FmtTokens(int64(u.CacheCreationInputTokens))))
	}
	if u.CacheCreation.Ephemeral1hInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("cc_1h=%s", display.FmtTokens(int64(u.CacheCreation.Ephemeral1hInputTokens))))
	}
	if tokens, window := contextEstimate(u, model); tokens > 0 {
		if window > 0 {
			parts = append(parts, fmt.Sprintf("ctx=%s/%s (%d%%)",
				display.FmtTokens(int64(tokens)), display.FmtTokens(int64(window)), tokens*100/window))
		} else {
			parts = append(parts, fmt.Sprintf("ctx=%s", display.FmtTokens(int64(tokens))))
		}
	}
	return parts
}

// rateLimitSummary renders a RateLimitLine into a header line and (verbose-only)
// extra lines. Each formatter applies its own styling around these strings.
func rateLimitSummary(e *RateLimitLine) (header string, verboseExtras []string) {
	parts := []string{"rate_limit:"}
	if e.RateLimitType != "" {
		parts = append(parts, e.RateLimitType)
	}
	if e.Status != "" {
		parts = append(parts, e.Status)
	}
	var extras []string
	if e.ResetsAt > 0 {
		until := time.Until(time.Unix(e.ResetsAt, 0))
		if until > 0 {
			extras = append(extras, fmt.Sprintf("resets in %s", FormatDuration(until)))
		} else {
			extras = append(extras, "resets now")
		}
	}
	if e.OverageStatus != "" {
		extras = append(extras, "overage "+e.OverageStatus)
	}
	if e.IsUsingOverage {
		extras = append(extras, "using overage")
	}
	header = strings.Join(parts, " ")
	if len(extras) > 0 {
		header += " (" + strings.Join(extras, ", ") + ")"
	}

	if e.ResetsAt > 0 {
		verboseExtras = append(verboseExtras,
			"resetsAt="+time.Unix(e.ResetsAt, 0).Format("2006-01-02 15:04:05"))
	}
	if e.OverageDisabledReason != "" {
		verboseExtras = append(verboseExtras, "overageDisabledReason="+e.OverageDisabledReason)
	}
	return
}
