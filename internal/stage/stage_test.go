package stage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ateam/internal/runner"
)

// fakeExecutor records what Execute was called with and returns the
// pre-configured summary. Stand-in for *runner.AgentExecutor in the
// dispatch-logic tests.
type fakeExecutor struct {
	called      bool
	gotCtx      context.Context
	gotProm     string
	gotOpts     runner.RunOpts
	gotProgress chan<- runner.RunProgress
	ret         runner.RunSummary
}

func (f *fakeExecutor) Execute(ctx context.Context, prompt string, opts runner.RunOpts, p chan<- runner.RunProgress) runner.RunSummary {
	f.called = true
	f.gotCtx = ctx
	f.gotProm = prompt
	f.gotOpts = opts
	f.gotProgress = p
	return f.ret
}

// recordingAction appends its tag to a shared slice on Ctx.Extra so test
// cases can assert the order Pre and Post actions ran in.
type recordingAction struct {
	tag string
	err error
}

func (r recordingAction) Run(c *Ctx) error {
	if c.Extra == nil {
		c.Extra = map[string]any{}
	}
	trace, _ := c.Extra["trace"].([]string)
	c.Extra["trace"] = append(trace, r.tag)
	return r.err
}

// setExecutor is a Pre action that pokes a configured Executor onto Ctx,
// matching what actions.ResolveExecutor will do for real stages.
type setExecutor struct{ e Executor }

func (s setExecutor) Run(c *Ctx) error {
	c.Executor = s.e
	return nil
}

func trace(c *Ctx) []string {
	t, _ := c.Extra["trace"].([]string)
	return t
}

func newCtx() *Ctx {
	return &Ctx{Context: context.Background()}
}

func makeStage(executor Executor, pre, post []Action) Stage {
	return Stage{
		Name:   "test",
		Action: "test",
		Pre: append([]Action{
			setExecutor{e: executor},
		}, pre...),
		BuildPrompt: func(c *Ctx) (string, error) {
			recordingAction{tag: "BuildPrompt"}.Run(c)
			return "PROMPT", nil
		},
		BuildRunOpts: func(*Ctx) runner.RunOpts {
			return runner.RunOpts{Action: "test"}
		},
		Post: post,
	}
}

func TestRunInvokesPreBuildExecutePostInOrder(t *testing.T) {
	exec := &fakeExecutor{ret: runner.RunSummary{ExecID: 42}}
	s := makeStage(exec,
		[]Action{recordingAction{tag: "pre1"}, recordingAction{tag: "pre2"}},
		[]Action{recordingAction{tag: "post1"}, recordingAction{tag: "post2"}},
	)
	c := newCtx()
	if err := Run(s, c); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"pre1", "pre2", "BuildPrompt", "post1", "post2"}
	got := trace(c)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
	if !exec.called {
		t.Error("Executor.Execute was not called")
	}
	if exec.gotProm != "PROMPT" {
		t.Errorf("Execute prompt = %q, want %q", exec.gotProm, "PROMPT")
	}
	if c.Result == nil || c.Result.ExecID != 42 {
		t.Errorf("c.Result not populated correctly: %+v", c.Result)
	}
}

func TestRunErrSkipEndsStageSuccessfully(t *testing.T) {
	exec := &fakeExecutor{}
	s := makeStage(exec,
		[]Action{
			recordingAction{tag: "pre1"},
			recordingAction{tag: "skipper", err: ErrSkip},
			recordingAction{tag: "pre-after-skip"}, // must NOT run
		},
		[]Action{recordingAction{tag: "post1"}}, // must NOT run
	)
	c := newCtx()
	if err := Run(s, c); err != nil {
		t.Fatalf("ErrSkip should be swallowed as success, got %v", err)
	}
	for _, tag := range trace(c) {
		if tag == "BuildPrompt" || tag == "pre-after-skip" || tag == "post1" {
			t.Errorf("%q should not have run after ErrSkip; trace=%v", tag, trace(c))
		}
	}
	if exec.called {
		t.Error("Executor.Execute should not have been called after ErrSkip")
	}
}

func TestRunPreErrorAbortsBeforeExecute(t *testing.T) {
	exec := &fakeExecutor{}
	boom := errors.New("nope")
	s := makeStage(exec,
		[]Action{recordingAction{tag: "boom", err: boom}},
		[]Action{recordingAction{tag: "post"}},
	)
	c := newCtx()
	err := Run(s, c)
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want wrap of boom", err)
	}
	if exec.called {
		t.Error("Executor.Execute should not have been called after a Pre error")
	}
	for _, tag := range trace(c) {
		if tag == "BuildPrompt" || tag == "post" {
			t.Errorf("%q should not have run after Pre error; trace=%v", tag, trace(c))
		}
	}
}

func TestRunPostErrorAbortsRemainingPost(t *testing.T) {
	exec := &fakeExecutor{ret: runner.RunSummary{ExecID: 1}}
	boom := errors.New("post-fail")
	s := makeStage(exec,
		nil,
		[]Action{
			recordingAction{tag: "post1"},
			recordingAction{tag: "post-boom", err: boom},
			recordingAction{tag: "post-after"}, // must NOT run
		},
	)
	c := newCtx()
	err := Run(s, c)
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want wrap of boom", err)
	}
	for _, tag := range trace(c) {
		if tag == "post-after" {
			t.Errorf("post-after should not have run; trace=%v", trace(c))
		}
	}
}

func TestRunMissingExecutorErrors(t *testing.T) {
	// No Pre action sets Ctx.Executor.
	s := Stage{
		Name:         "test",
		BuildPrompt:  func(*Ctx) (string, error) { return "p", nil },
		BuildRunOpts: func(*Ctx) runner.RunOpts { return runner.RunOpts{} },
	}
	err := Run(s, newCtx())
	if err == nil || !strings.Contains(err.Error(), "no executor") {
		t.Errorf("expected 'no executor' error, got %v", err)
	}
}

func TestRunValidatesRequiredHooks(t *testing.T) {
	cases := []struct {
		name string
		s    Stage
		want string
	}{
		{"missing BuildPrompt", Stage{Name: "x", BuildRunOpts: func(*Ctx) runner.RunOpts { return runner.RunOpts{} }}, "BuildPrompt"},
		{"missing BuildRunOpts", Stage{Name: "x", BuildPrompt: func(*Ctx) (string, error) { return "p", nil }}, "BuildRunOpts"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Run(c.s, newCtx())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("expected %q in error, got %v", c.want, err)
			}
		})
	}
}

func TestRunNilCtxErrors(t *testing.T) {
	s := Stage{
		Name:         "x",
		BuildPrompt:  func(*Ctx) (string, error) { return "p", nil },
		BuildRunOpts: func(*Ctx) runner.RunOpts { return runner.RunOpts{} },
	}
	if err := Run(s, nil); err == nil {
		t.Error("expected error for nil Ctx, got nil")
	}
}

func TestRunForwardsProgressChannel(t *testing.T) {
	exec := &fakeExecutor{}
	s := makeStage(exec, nil, nil)
	ch := make(chan runner.RunProgress, 1)
	c := newCtx()
	c.Progress = ch
	if err := Run(s, c); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exec.gotProgress == nil {
		t.Error("Executor.Execute received nil progress; expected the chan from Ctx.Progress")
	}
}

func TestRunForwardsNilProgressByDefault(t *testing.T) {
	exec := &fakeExecutor{}
	s := makeStage(exec, nil, nil)
	c := newCtx() // no Progress set
	if err := Run(s, c); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exec.gotProgress != nil {
		t.Error("Executor.Execute received a non-nil progress; expected nil when Ctx.Progress is unset")
	}
}

func TestRunBuildPromptErrorAborts(t *testing.T) {
	exec := &fakeExecutor{}
	boom := errors.New("assemble failed")
	s := Stage{
		Name:        "test",
		BuildPrompt: func(*Ctx) (string, error) { return "", boom },
		BuildRunOpts: func(*Ctx) runner.RunOpts {
			return runner.RunOpts{Action: "test"}
		},
		Pre:  []Action{setExecutor{e: exec}},
		Post: []Action{recordingAction{tag: "post"}},
	}
	c := newCtx()
	err := Run(s, c)
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want wrap of boom", err)
	}
	if exec.called {
		t.Error("Executor.Execute should not have been called after BuildPrompt error")
	}
	for _, tag := range trace(c) {
		if tag == "post" {
			t.Errorf("post should not have run after BuildPrompt error; trace=%v", trace(c))
		}
	}
}
