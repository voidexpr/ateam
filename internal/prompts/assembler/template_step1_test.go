package assembler

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// stubDispatcher captures the (name, args) the engine forwards and returns a
// canned reply or error.
type stubDispatcher struct {
	gotName string
	gotArgs []string
	reply   string
	err     error
}

func (d *stubDispatcher) Dispatch(name string, args []string) (string, error) {
	d.gotName = name
	d.gotArgs = append([]string(nil), args...)
	return d.reply, d.err
}

func TestRenderVarDefault(t *testing.T) {
	e := NewEngine(nil, 0)
	v := mkVars()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty value falls back to default", "{{env.EMPTY ? fallback}}", "fallback"},
		{"non-empty value wins over default", "{{prompt.name ? other}}", "security"},
		{"default is rendered against vars", "{{env.EMPTY ? {{prompt.name}}}}", "security"},
		{"default left-trimmed; directive's outer whitespace stripped", "{{env.EMPTY ?   x  }}", "x"},
		{"unknown namespace + default passes through", "{{foo.bar ? hi}}", "{{foo.bar ? hi}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := e.Render(tc.in, v)
			if err != nil {
				t.Fatalf("Render err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderVarDefaultUnknownKeyStillErrors(t *testing.T) {
	// Typos in a known namespace error regardless of `? default` — the
	// default only kicks in for empty values, not lookup failures.
	e := NewEngine(nil, 0)
	_, err := e.Render("{{prompt.nope ? whatever}}", mkVars())
	if err == nil || !strings.Contains(err.Error(), "prompt.nope") {
		t.Fatalf("expected unknown-key error, got %v", err)
	}
}

func TestRenderIncludeFallback(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"present.md": "PRESENT-{{prompt.name}}"},
	)
	a := New(anchors)
	e := NewEngine(a, 0)
	v := mkVars()

	t.Run("missing required + fallback renders TEXT", func(t *testing.T) {
		got, err := e.Render("{{include missing.md ? FB-{{prompt.name}}}}", v)
		if err != nil {
			t.Fatal(err)
		}
		if got != "FB-security" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("present file ignores fallback", func(t *testing.T) {
		got, err := e.Render("{{include present.md ? FB}}", v)
		if err != nil {
			t.Fatal(err)
		}
		if got != "PRESENT-security" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("optional include + fallback uses fallback when missing", func(t *testing.T) {
		got, err := e.Render("{{include? missing.md ? optional-FB}}", v)
		if err != nil {
			t.Fatal(err)
		}
		if got != "optional-FB" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestRenderIncludeQuotedPath(t *testing.T) {
	anchors := mkAnchors(
		nil, nil,
		map[string]string{"a b.md": "spaced-content"},
	)
	a := New(anchors)
	e := NewEngine(a, 0)

	t.Run("quoted path with whitespace resolves", func(t *testing.T) {
		got, err := e.Render(`{{include "a b.md"}}`, mkVars())
		if err != nil {
			t.Fatal(err)
		}
		if got != "spaced-content" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("unquoted path with whitespace errors", func(t *testing.T) {
		_, err := e.Render("{{include a b.md}}", mkVars())
		if err == nil || !strings.Contains(err.Error(), "one path arg") {
			t.Fatalf("expected too-many-args error, got %v", err)
		}
	})
}

func TestRenderDynamicDispatchOff(t *testing.T) {
	// With no dispatcher configured, dynamic.* passes through verbatim.
	e := NewEngine(nil, 0)
	got, err := e.Render("call {{dynamic.foo a b}}", mkVars())
	if err != nil {
		t.Fatal(err)
	}
	if got != "call {{dynamic.foo a b}}" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderDynamicDispatch(t *testing.T) {
	d := &stubDispatcher{reply: "<result>"}
	e := NewEngine(nil, 0).WithDispatcher(d)
	got, err := e.Render(`{{dynamic.hello world {{prompt.name}} "two words"}}`, mkVars())
	if err != nil {
		t.Fatal(err)
	}
	if got != "<result>" {
		t.Fatalf("got %q", got)
	}
	if d.gotName != "hello" {
		t.Fatalf("name = %q", d.gotName)
	}
	want := []string{"world", "security", "two words"}
	if !reflect.DeepEqual(d.gotArgs, want) {
		t.Fatalf("args = %#v, want %#v", d.gotArgs, want)
	}
}

func TestRenderDynamicNoArgs(t *testing.T) {
	d := &stubDispatcher{reply: "ok"}
	e := NewEngine(nil, 0).WithDispatcher(d)
	if _, err := e.Render("{{dynamic.bare}}", mkVars()); err != nil {
		t.Fatal(err)
	}
	if d.gotName != "bare" {
		t.Fatalf("name = %q", d.gotName)
	}
	if len(d.gotArgs) != 0 {
		t.Fatalf("args = %#v, want empty", d.gotArgs)
	}
}

func TestRenderDynamicError(t *testing.T) {
	d := &stubDispatcher{err: errors.New("boom")}
	e := NewEngine(nil, 0).WithDispatcher(d)
	_, err := e.Render("{{dynamic.x}}", mkVars())
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want boom, got %v", err)
	}
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		err  bool
	}{
		{"", []string{}, false},
		{"a b c", []string{"a", "b", "c"}, false},
		{`"a b" c`, []string{"a b", "c"}, false},
		{`'a b' c`, []string{"a b", "c"}, false},
		{`"a\"b"`, []string{`a"b`}, false},
		{`'a\'b'`, []string{`a'b`}, false},
		{`"a\\b"`, []string{`a\b`}, false},
		{`"a\nb"`, []string{`a\nb`}, false}, // unsupported escape kept literal
		{`one\two`, []string{`one\two`}, false},
		{`  leading and  trailing  `, []string{"leading", "and", "trailing"}, false},
		{`mix "two words" 'and more' end`, []string{"mix", "two words", "and more", "end"}, false},
		{`"unterminated`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := tokenize(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("want error, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			// Normalize empty slice vs nil for compare.
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSplitFallback(t *testing.T) {
	cases := []struct {
		in        string
		wantHead  string
		wantTail  string
		wantSplit bool
	}{
		{"abc", "abc", "", false},
		{"foo ? bar", "foo", "bar", true},
		{"foo ?bar", "foo ?bar", "", false}, // requires space on both sides
		{"foo? bar", "foo? bar", "", false}, // ditto
		{"{{x ? y}} rest", "{{x ? y}} rest", "", false},
		{"a {{x ? y}} ? z", "a {{x ? y}}", "z", true},
		{`"a ? b" ? c`, `"a ? b"`, "c", true},
		{`'a ? b'`, `'a ? b'`, "", false},
		{"foo  ?  bar", "foo", "bar", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			gotHead, gotTail, ok := splitFallback(tc.in)
			if ok != tc.wantSplit || gotHead != tc.wantHead || gotTail != tc.wantTail {
				t.Fatalf("split(%q) = (%q, %q, %v); want (%q, %q, %v)",
					tc.in, gotHead, gotTail, ok, tc.wantHead, tc.wantTail, tc.wantSplit)
			}
		})
	}
}

func TestRenderDispatcherCallsRespectVarExpansion(t *testing.T) {
	// Args are expanded BEFORE tokenization. An expanded value containing
	// whitespace fans out into multiple tokens unless the placeholder was
	// quoted in the template.
	v := MapVars{
		Prompt: map[string]string{
			"name":  "x",
			"multi": "a b c",
		},
		EnvLookup: func(string) (string, bool) { return "", false },
	}
	d := &stubDispatcher{reply: "ok"}
	e := NewEngine(nil, 0).WithDispatcher(d)

	t.Run("unquoted multi-word var fans out", func(t *testing.T) {
		_, err := e.Render("{{dynamic.fn {{prompt.multi}}}}", v)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"a", "b", "c"}
		if !reflect.DeepEqual(d.gotArgs, want) {
			t.Fatalf("args = %#v, want %#v", d.gotArgs, want)
		}
	})

	t.Run("quoted multi-word var stays a single token", func(t *testing.T) {
		_, err := e.Render(`{{dynamic.fn "{{prompt.multi}}"}}`, v)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"a b c"}
		if !reflect.DeepEqual(d.gotArgs, want) {
			t.Fatalf("args = %#v, want %#v", d.gotArgs, want)
		}
	})
}
