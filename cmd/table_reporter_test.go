package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ateam/internal/flow"
	"github.com/ateam/internal/runner"
)

// newTestTableReporter builds a tableReporter that never touches the real
// terminal (quiet=true forces useLive=false), with a discard sink so the
// summary line written by Close doesn't pollute test output.
func newTestTableReporter(labels []string) *tableReporter {
	return newTableReporter(tableReporterOpts{
		out:       &bytes.Buffer{},
		labels:    labels,
		agentName: "test-agent",
		itemLabel: "role(s)",
		quiet:     true,
	})
}

// TestTableReporterFullyBlockedExitsNonZero locks in the d18efca fix:
// when PreDispatch blocks every bundle so no BundleStart/BundleEnd ever
// fires, StageEnd must mark all queued rows skipped and Close must return
// a non-nil error reflecting that — otherwise a fully budget-blocked
// `ateam report` would exit 0 with "0 succeeded, 0 failed".
func TestTableReporterFullyBlockedExitsNonZero(t *testing.T) {
	labels := []string{"role-a", "role-b", "role-c"}
	tr := newTestTableReporter(labels)

	tr.StageEnd(flow.StageInfo{}, flow.StageOutcome{})
	err := tr.Close()

	succeeded, failed, skipped := tr.Counts()
	if succeeded != 0 || failed != 0 || skipped != len(labels) {
		t.Errorf("Counts() = (%d, %d, %d), want (0, 0, %d)",
			succeeded, failed, skipped, len(labels))
	}
	if err == nil {
		t.Fatal("Close() returned nil error, want non-nil for fully blocked stage")
	}
	if !strings.Contains(err.Error(), "3 ") || !strings.Contains(err.Error(), "skipped") {
		t.Errorf("Close() error = %q, want it to mention skip count and word 'skipped'", err)
	}
}

// TestTableReporterPartialDispatch covers the mixed case: some bundles
// run to completion, the rest are blocked before BundleStart. StageEnd
// must skip-count only the queued tail, and Close must still return an
// error because skipped > 0.
func TestTableReporterPartialDispatch(t *testing.T) {
	labels := []string{"role-a", "role-b", "role-c"}
	tr := newTestTableReporter(labels)

	info := flow.BundleInfo{Name: "role-a"}
	tr.BundleStart(info)
	tr.BundleEnd(info, flow.Result{
		Flow:    flow.Flow{State: flow.StateContinue},
		Summary: &runner.RunSummary{RoleID: "role-a"},
	})

	tr.StageEnd(flow.StageInfo{}, flow.StageOutcome{})
	err := tr.Close()

	succeeded, failed, skipped := tr.Counts()
	if succeeded != 1 || failed != 0 || skipped != 2 {
		t.Errorf("Counts() = (%d, %d, %d), want (1, 0, 2)",
			succeeded, failed, skipped)
	}
	if err == nil {
		t.Fatal("Close() returned nil error, want non-nil when any row was skipped")
	}
	if !strings.Contains(err.Error(), "skipped") {
		t.Errorf("Close() error = %q, want it to mention 'skipped'", err)
	}
}

// TestTableReporterAllDispatched is the clean happy path: every row
// completes successfully. StageEnd has no queued rows to upgrade and
// Close must return nil.
func TestTableReporterAllDispatched(t *testing.T) {
	labels := []string{"role-a", "role-b", "role-c"}
	tr := newTestTableReporter(labels)

	for _, name := range labels {
		info := flow.BundleInfo{Name: name}
		tr.BundleStart(info)
		tr.BundleEnd(info, flow.Result{
			Flow:    flow.Flow{State: flow.StateContinue},
			Summary: &runner.RunSummary{RoleID: name},
		})
	}

	tr.StageEnd(flow.StageInfo{}, flow.StageOutcome{})
	err := tr.Close()

	succeeded, failed, skipped := tr.Counts()
	if succeeded != len(labels) || failed != 0 || skipped != 0 {
		t.Errorf("Counts() = (%d, %d, %d), want (%d, 0, 0)",
			succeeded, failed, skipped, len(labels))
	}
	if err != nil {
		t.Errorf("Close() = %v, want nil when all rows succeeded", err)
	}
}
