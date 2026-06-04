package prompts

import (
	"errors"
	"strings"
	"testing"

	"github.com/ateam/internal/root"
)

type stubCtx struct {
	env  *root.ResolvedEnv
	vars Vars
	mode ResolveMode
	dyn  PromptDynamic
}

func (s *stubCtx) Env() *root.ResolvedEnv  { return s.env }
func (s *stubCtx) Vars() Vars              { return s.vars }
func (s *stubCtx) Mode() ResolveMode       { return s.mode }
func (s *stubCtx) Dynamics() PromptDynamic { return s.dyn }

func TestDispatcherCallsFunction(t *testing.T) {
	ctx := &stubCtx{mode: ModeReal}
	var got struct {
		name string
		args []string
		mode ResolveMode
	}
	funcs := PromptDynamic{
		"echo": func(c ResolveContext, args ...string) (string, error) {
			got.name = "echo"
			got.args = append([]string(nil), args...)
			got.mode = c.Mode()
			return strings.Join(args, "+"), nil
		},
	}
	d := NewDispatcher(funcs, ctx)
	out, err := d.Dispatch("echo", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "a+b" {
		t.Fatalf("out = %q", out)
	}
	if got.name != "echo" || got.mode != ModeReal {
		t.Fatalf("got = %+v", got)
	}
}

func TestDispatcherUnknownErrors(t *testing.T) {
	d := NewDispatcher(PromptDynamic{}, &stubCtx{})
	_, err := d.Dispatch("nope", nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("want unknown-dynamic error, got %v", err)
	}
}

func TestDispatcherSurfacesFunctionError(t *testing.T) {
	want := errors.New("kaboom")
	d := NewDispatcher(PromptDynamic{
		"x": func(ResolveContext, ...string) (string, error) { return "", want },
	}, &stubCtx{})
	_, err := d.Dispatch("x", nil)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
