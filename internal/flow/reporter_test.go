package flow

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ateam/internal/runner"
)

func TestStdoutReporter_BundleEndSuccess(t *testing.T) {
	var buf bytes.Buffer
	r := &StdoutReporter{Out: &buf}

	bi := BundleInfo{Name: "verify", Role: "supervisor", Action: "verify"}
	r.BundleEnd(bi, Result{
		Flow:    Flow{State: StateContinue},
		Summary: &runner.RunSummary{Duration: 12 * time.Second, Cost: 0.012},
	})

	if got := buf.String(); !strings.Contains(got, "Done (") {
		t.Errorf("expected 'Done (' in output: %q", got)
	}
}

func TestStdoutReporter_BundleStartIsNoOp(t *testing.T) {
	// Cmd-layer owns the starting line; StdoutReporter must not duplicate.
	var buf bytes.Buffer
	r := &StdoutReporter{Out: &buf}
	r.BundleStart(BundleInfo{Name: "verify", Role: "supervisor"})
	if got := buf.String(); got != "" {
		t.Errorf("BundleStart should be silent; got %q", got)
	}
}

func TestStdoutReporter_SkipAndError(t *testing.T) {
	cases := []struct {
		name     string
		res      Result
		contains string
	}{
		{
			name: "skip",
			res: Result{
				Flow: Flow{State: StateSkip, Reason: "nothing stale"},
			},
			contains: "Skipped verify: nothing stale",
		},
		{
			name: "error-with-reason",
			res: Result{
				Flow: Flow{State: StateError, Reason: "agent failed"},
			},
			contains: "Failed verify: agent failed",
		},
		{
			name: "error-fallback-to-err",
			res: Result{
				Flow: Flow{State: StateError, Err: errors.New("io explode")},
			},
			contains: "io explode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			r := &StdoutReporter{Out: &buf}
			r.BundleEnd(BundleInfo{Name: "verify"}, tc.res)
			if got := buf.String(); !strings.Contains(got, tc.contains) {
				t.Errorf("output %q missing %q", got, tc.contains)
			}
		})
	}
}

func TestStdoutReporter_AgentEventNoOp(t *testing.T) {
	// StdoutReporter intentionally ignores AgentEvent — the runner already
	// streams subprocess output. Verify nothing is written.
	var buf bytes.Buffer
	r := &StdoutReporter{Out: &buf}
	r.AgentEvent(BundleInfo{Name: "verify"}, runner.RunProgress{Phase: "tool", ToolName: "Read"})
	if got := buf.String(); got != "" {
		t.Errorf("AgentEvent should be silent on StdoutReporter; got %q", got)
	}
}

func TestBaseReporter_AllNoOp(t *testing.T) {
	// Sanity: BaseReporter methods don't panic and do nothing.
	var r BaseReporter
	r.StageStart(StageInfo{})
	r.StageEnd(StageInfo{}, StageOutcome{})
	r.StepSkipped(StageInfo{}, "x", "y")
	r.BundleStart(BundleInfo{})
	r.BundleEnd(BundleInfo{}, Result{})
	r.AgentEvent(BundleInfo{}, runner.RunProgress{})
}
