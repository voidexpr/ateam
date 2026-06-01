package flow

import (
	"reflect"
	"testing"

	"github.com/ateam/internal/runner"
)

// tracingReporter captures the sequence of callbacks it receives,
// tagged with the reporter's name so MultiReporter ordering can be
// asserted across multiple children.
type tracingReporter struct {
	BaseReporter
	name   string
	events *[]string
}

func (r *tracingReporter) StageStart(s StageInfo) {
	*r.events = append(*r.events, r.name+":StageStart:"+s.Name)
}
func (r *tracingReporter) StageEnd(s StageInfo, _ StageOutcome) {
	*r.events = append(*r.events, r.name+":StageEnd:"+s.Name)
}
func (r *tracingReporter) StepSkipped(_ StageInfo, step, _ string) {
	*r.events = append(*r.events, r.name+":StepSkipped:"+step)
}
func (r *tracingReporter) BundleStart(b BundleInfo) {
	*r.events = append(*r.events, r.name+":BundleStart:"+b.Name)
}
func (r *tracingReporter) BundleEnd(b BundleInfo, _ Result) {
	*r.events = append(*r.events, r.name+":BundleEnd:"+b.Name)
}
func (r *tracingReporter) AgentEvent(b BundleInfo, _ runner.RunProgress) {
	*r.events = append(*r.events, r.name+":AgentEvent:"+b.Name)
}

func TestMultiReporter_FansAllCallbacks(t *testing.T) {
	var events []string
	a := &tracingReporter{name: "A", events: &events}
	b := &tracingReporter{name: "B", events: &events}

	m := MultiReporter{a, b}

	si := StageInfo{Name: "pipe"}
	bi := BundleInfo{Name: "bundle"}

	m.StageStart(si)
	m.BundleStart(bi)
	m.AgentEvent(bi, runner.RunProgress{})
	m.StepSkipped(si, "step2", "earlier failed")
	m.BundleEnd(bi, Result{})
	m.StageEnd(si, StageOutcome{})

	want := []string{
		"A:StageStart:pipe", "B:StageStart:pipe",
		"A:BundleStart:bundle", "B:BundleStart:bundle",
		"A:AgentEvent:bundle", "B:AgentEvent:bundle",
		"A:StepSkipped:step2", "B:StepSkipped:step2",
		"A:BundleEnd:bundle", "B:BundleEnd:bundle",
		"A:StageEnd:pipe", "B:StageEnd:pipe",
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("event ordering mismatch\n got: %v\nwant: %v", events, want)
	}
}

func TestMultiReporter_SkipsNilChildren(t *testing.T) {
	var events []string
	a := &tracingReporter{name: "A", events: &events}
	b := &tracingReporter{name: "B", events: &events}

	m := MultiReporter{nil, a, nil, b, nil}

	m.BundleStart(BundleInfo{Name: "x"})
	m.BundleEnd(BundleInfo{Name: "x"}, Result{})

	want := []string{
		"A:BundleStart:x", "B:BundleStart:x",
		"A:BundleEnd:x", "B:BundleEnd:x",
	}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("nil-skip mismatch\n got: %v\nwant: %v", events, want)
	}
}

func TestMultiReporter_EmptyIsNoOp(t *testing.T) {
	// An empty MultiReporter is a valid no-op Reporter — it must not
	// panic when called.
	var m MultiReporter
	m.StageStart(StageInfo{})
	m.StageEnd(StageInfo{}, StageOutcome{})
	m.StepSkipped(StageInfo{}, "x", "y")
	m.BundleStart(BundleInfo{})
	m.BundleEnd(BundleInfo{}, Result{})
	m.AgentEvent(BundleInfo{}, runner.RunProgress{})
}

func TestMultiReporter_PreservesDeclarationOrder(t *testing.T) {
	// Asserts ordering matters: B-then-A produces the reverse trace of
	// A-then-B. Children are not deduplicated or reordered.
	var events []string
	a := &tracingReporter{name: "A", events: &events}
	b := &tracingReporter{name: "B", events: &events}

	m := MultiReporter{b, a}
	m.BundleStart(BundleInfo{Name: "z"})

	want := []string{"B:BundleStart:z", "A:BundleStart:z"}
	if !reflect.DeepEqual(events, want) {
		t.Errorf("declaration order not preserved\n got: %v\nwant: %v", events, want)
	}
}
