package runner

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ateam/internal/agent"
)

// fakeProgressAgent emits a fixed sequence of assistant events with
// cumulative tokens + cost so we can assert the runner forwards them
// onto RunProgress.
type fakeProgressAgent struct{}

func (*fakeProgressAgent) Name() string                                               { return "fake" }
func (*fakeProgressAgent) SetModel(string)                                            {}
func (a *fakeProgressAgent) CloneWithResolvedTemplates(*strings.Replacer) agent.Agent { return a }
func (*fakeProgressAgent) DebugCommandArgs([]string) (string, []string)               { return "fake", nil }

func (*fakeProgressAgent) Run(ctx context.Context, req agent.Request) <-chan agent.StreamEvent {
	ch := make(chan agent.StreamEvent, 8)
	go func() {
		defer close(ch)
		ch <- agent.StreamEvent{Type: "system", SessionID: "s1", Model: "m1"}
		ch <- agent.StreamEvent{
			Type: "assistant", Text: "hi",
			InputTokens: 100, OutputTokens: 20, Cost: 0.05, Model: "m1",
		}
		ch <- agent.StreamEvent{
			Type: "assistant", Text: "more",
			InputTokens: 150, OutputTokens: 50, Cost: 0.25, Model: "m1",
		}
		ch <- agent.StreamEvent{
			Type:         "result",
			Output:       "more",
			InputTokens:  150,
			OutputTokens: 50,
			Cost:         0.25,
			Turns:        1,
			Model:        "m1",
		}
	}()
	return ch
}

func TestRunnerForwardsCumulativeProgress(t *testing.T) {
	dir := t.TempDir()
	r := &Runner{Agent: &fakeProgressAgent{}}

	opts := RunOpts{
		RoleID:  "fake",
		Action:  ActionRun,
		LogsDir: filepath.Join(dir, "logs"),
	}

	progressCh := make(chan RunProgress, 32)
	summary := r.Run(context.Background(), "prompt", opts, progressCh)
	close(progressCh)

	if summary.Err != nil {
		t.Fatalf("unexpected err: %v", summary.Err)
	}

	var peakInput, peakOutput int
	var peakCost float64
	var sawAssistant bool
	for p := range progressCh {
		if p.Phase == PhaseThinking {
			sawAssistant = true
		}
		if p.CumulativeInputTokens > peakInput {
			peakInput = p.CumulativeInputTokens
		}
		if p.CumulativeOutputTokens > peakOutput {
			peakOutput = p.CumulativeOutputTokens
		}
		if p.EstimatedCost > peakCost {
			peakCost = p.EstimatedCost
		}
	}

	if !sawAssistant {
		t.Fatal("expected at least one assistant-phase progress event")
	}
	if peakInput != 150 {
		t.Errorf("CumulativeInputTokens peak = %d, want 150", peakInput)
	}
	if peakOutput != 50 {
		t.Errorf("CumulativeOutputTokens peak = %d, want 50", peakOutput)
	}
	if peakCost < 0.249 || peakCost > 0.251 {
		t.Errorf("EstimatedCost peak = %f, want ~0.25", peakCost)
	}
}
