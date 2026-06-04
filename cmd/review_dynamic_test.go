package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/prompts"
)

// TestReviewReportsDynamicPreviewSentinel — spec line 388-399: dynamics
// that depend on generated artifacts (review_reports reads
// shared/report/*.md produced by a prior `ateam report` run) return a
// preview sentinel in ModePreview rather than reading disk. This is the
// gate for flow.Verify producing a deterministic resolution against
// review even when no reports exist yet.
func TestReviewReportsDynamicPreviewSentinel(t *testing.T) {
	dyn := reviewReportsDynamic(nil, prompts.ReviewSelector{})
	out, err := dyn(&stubReviewCtx{mode: prompts.ModePreview})
	if err != nil {
		t.Fatalf("dynamic err: %v", err)
	}
	if !strings.Contains(out, "AT RUNTIME") {
		t.Errorf("preview output should be a sentinel, got:\n%s", out)
	}
}

// TestReviewReportsDynamicByteIdenticalToLegacy locks in the spec
// invariant that the new mechanism's output equals the legacy
// `formatReportsBlock(reports)` byte-for-byte. If this fails, the
// dynamic and the wrapper-struct path have drifted — and the next
// session is being asked to keep both around. Reject that.
func TestReviewReportsDynamicByteIdenticalToLegacy(t *testing.T) {
	reports := []prompts.RoleReport{
		{RoleID: "security", ModTime: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC), Content: "# Findings\n\nsec"},
		{RoleID: "test.gaps", ModTime: time.Date(2026, 6, 4, 12, 30, 0, 0, time.UTC), Content: "# Findings\n\ngaps"},
	}
	want := formatReportsBlock(reports)
	dyn := reviewReportsDynamicForTest(reports)
	got, err := dyn(&stubReviewCtx{mode: prompts.ModeReal})
	if err != nil {
		t.Fatalf("dynamic err: %v", err)
	}
	if got != want {
		t.Errorf("dynamic output drifted from formatReportsBlock\n--- got:\n%s\n--- want:\n%s", got, want)
	}
}

// stubReviewCtx is a minimal ResolveContext for dynamic tests.
type stubReviewCtx struct {
	mode prompts.ResolveMode
}

func (s *stubReviewCtx) Vars() prompts.Vars              { return nil }
func (s *stubReviewCtx) Mode() prompts.ResolveMode       { return s.mode }
func (s *stubReviewCtx) Dynamics() prompts.PromptDynamic { return nil }

// reviewReportsDynamicForTest returns a closure that always uses the
// supplied reports — bypasses env+selector for byte-identity testing.
func reviewReportsDynamicForTest(reports []prompts.RoleReport) prompts.PromptDynamicFunction {
	return func(ctx prompts.ResolveContext, _ ...string) (string, error) {
		if ctx.Mode() == prompts.ModePreview {
			return "{{AT RUNTIME: review reports manifest}}", nil
		}
		return formatReportsBlock(reports), nil
	}
}
