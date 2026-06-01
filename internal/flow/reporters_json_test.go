package flow

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ateam/internal/runner"
)

// parseJSONL decodes every JSONL line of buf into a slice of maps.
func parseJSONL(t *testing.T, buf []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range bytes.Split(buf, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestJSONReporter_BundleAndAgentInterleaved(t *testing.T) {
	var buf bytes.Buffer
	rep := &JSONReporter{W: &buf}

	dir := t.TempDir()
	exec := &preparingExec{logsRoot: dir}
	bundle := PromptBundle{
		Name:    "exec",
		Render:  func(RuntimeEnv) (string, error) { return "hi", nil },
		RunOpts: func(RuntimeEnv) runner.RunOpts { return runner.RunOpts{RoleID: "tester"} },
	}
	env := RuntimeEnv{Executor: exec, Role: "tester", Action: "exec", WorkDir: "/wd", Batch: "b"}
	rc := RunCtx{Ctx: context.Background(), Reporter: rep}
	_ = RunBundle(bundle, env, rc)

	events := parseJSONL(t, buf.Bytes())
	if len(events) == 0 {
		t.Fatalf("no events written")
	}

	var sources []string
	var kinds []string
	for _, e := range events {
		sources = append(sources, e["source"].(string))
		if k, ok := e["kind"]; ok {
			kinds = append(kinds, k.(string))
		}
	}

	// At minimum: bundle_start + agent_exec_start + (>=1 agent) +
	// agent_exec_end + bundle_end. The preparingExec emits one agent
	// init event, so we expect 5 events total.
	wantKinds := []string{"bundle_start", "agent_exec_start", "agent_exec_end", "bundle_end"}
	for _, want := range wantKinds {
		found := false
		for _, k := range kinds {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing bundle kind %q in %v", want, kinds)
		}
	}

	// At least one agent-source event present.
	sawAgent := false
	for _, s := range sources {
		if s == "agent" {
			sawAgent = true
			break
		}
	}
	if !sawAgent {
		t.Errorf("no source=agent events in stream")
	}
}

func TestJSONReporter_VFieldStable(t *testing.T) {
	// Every event must carry v:1 and a numeric ts.
	var buf bytes.Buffer
	rep := &JSONReporter{W: &buf}
	rep.BundleStart(BundleInfo{Name: "x"})
	rep.BundleEnd(BundleInfo{Name: "x"}, Result{Flow: Flow{State: StateContinue}})

	events := parseJSONL(t, buf.Bytes())
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	for i, e := range events {
		if v, ok := e["v"].(float64); !ok || v != 1 {
			t.Errorf("event %d v field: got %v want 1", i, e["v"])
		}
		if _, ok := e["ts"].(float64); !ok {
			t.Errorf("event %d ts missing or non-numeric: %v", i, e["ts"])
		}
	}
}

func TestJSONReporter_ConcurrentWritesSerialize(t *testing.T) {
	// Multiple goroutines calling Reporter methods concurrently must not
	// interleave inside a single line — `\n` only appears as a record
	// separator, never mid-event. Race detector validates the mutex.
	var buf bytes.Buffer
	rep := &JSONReporter{W: &buf}

	var wg sync.WaitGroup
	const N = 50
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rep.BundleStart(BundleInfo{Name: "x"})
			rep.AgentEvent(BundleInfo{Name: "x"}, runner.RunProgress{Phase: "tool", ToolName: "Read", ExecID: int64(i)})
			rep.BundleEnd(BundleInfo{Name: "x"}, Result{Flow: Flow{State: StateContinue}})
		}(i)
	}
	wg.Wait()

	events := parseJSONL(t, buf.Bytes())
	if got, want := len(events), N*3; got != want {
		t.Errorf("expected %d events, got %d", want, got)
	}
}

func TestJSONReporter_ActionEvents(t *testing.T) {
	var buf bytes.Buffer
	rep := &JSONReporter{W: &buf}
	rep.ActionStart(BundleInfo{Name: "x"}, PreExec, "CheckConcurrentRuns", 0)
	rep.ActionEnd(BundleInfo{Name: "x"}, PreExec, "CheckConcurrentRuns", 0,
		Flow{State: StateContinue}, 12*time.Millisecond)

	events := parseJSONL(t, buf.Bytes())
	if events[0]["kind"] != "pre_exec_start" {
		t.Errorf("start kind: got %v want pre_exec_start", events[0]["kind"])
	}
	if events[1]["kind"] != "pre_exec_end" {
		t.Errorf("end kind: got %v want pre_exec_end", events[1]["kind"])
	}
	if events[1]["state"] != "continue" {
		t.Errorf("end state: got %v want continue", events[1]["state"])
	}
	if events[1]["duration_ms"].(float64) != 12 {
		t.Errorf("duration_ms: got %v want 12", events[1]["duration_ms"])
	}
	if events[1]["action_type"] != "CheckConcurrentRuns" {
		t.Errorf("action_type: got %v want CheckConcurrentRuns", events[1]["action_type"])
	}
}

func TestJSONReporter_AgentEventPayloadFields(t *testing.T) {
	var buf bytes.Buffer
	rep := &JSONReporter{W: &buf}
	rep.AgentEvent(BundleInfo{Name: "x"}, runner.RunProgress{
		ExecID:    42,
		Phase:     "tool",
		ToolName:  "Read",
		ToolInput: "/etc/hosts",
		ToolCount: 7,
		Elapsed:   250 * time.Millisecond,
	})

	events := parseJSONL(t, buf.Bytes())
	e := events[0]
	if e["source"] != "agent" {
		t.Errorf("source: got %v want agent", e["source"])
	}
	if int64(e["exec_id"].(float64)) != 42 {
		t.Errorf("exec_id: got %v want 42", e["exec_id"])
	}
	if e["tool_name"] != "Read" {
		t.Errorf("tool_name: got %v want Read", e["tool_name"])
	}
	if e["tool_input"] != "/etc/hosts" {
		t.Errorf("tool_input: got %v want /etc/hosts", e["tool_input"])
	}
	if e["tool_count"].(float64) != 7 {
		t.Errorf("tool_count: got %v want 7", e["tool_count"])
	}
	if e["elapsed_ms"].(float64) != 250 {
		t.Errorf("elapsed_ms: got %v want 250", e["elapsed_ms"])
	}
}
