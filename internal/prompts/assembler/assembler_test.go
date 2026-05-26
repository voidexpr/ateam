package assembler

import (
	"errors"
	"io/fs"
	"reflect"
	"testing"
	"testing/fstest"
)

// mkAnchors builds a [project, org, embedded] anchor list from three maps. nil
// maps become empty FSes so callers can skip an anchor by passing nil.
func mkAnchors(project, org, embedded map[string]string) []Anchor {
	mapToFS := func(m map[string]string) fs.FS {
		mfs := fstest.MapFS{}
		for k, v := range m {
			mfs[k] = &fstest.MapFile{Data: []byte(v)}
		}
		return mfs
	}
	return []Anchor{
		{Name: "project", FS: mapToFS(project)},
		{Name: "org", FS: mapToFS(org)},
		{Name: "embedded", FS: mapToFS(embedded)},
	}
}

func TestFirstMatch(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"report/security.prompt.md": "project version"},
		nil,
		map[string]string{
			"report/security.prompt.md": "embedded version",
			"review.prompt.md":          "embedded review",
		},
	)
	a := New(anchors)

	t.Run("project overrides embedded", func(t *testing.T) {
		m, ok, err := a.FirstMatch("report/security.prompt.md")
		if err != nil || !ok {
			t.Fatalf("FirstMatch err=%v ok=%v", err, ok)
		}
		if m.Anchor != "project" {
			t.Fatalf("anchor = %q, want project", m.Anchor)
		}
		if string(m.Content) != "project version" {
			t.Fatalf("content = %q", string(m.Content))
		}
	})

	t.Run("falls back to embedded", func(t *testing.T) {
		m, ok, err := a.FirstMatch("review.prompt.md")
		if err != nil || !ok {
			t.Fatalf("FirstMatch err=%v ok=%v", err, ok)
		}
		if m.Anchor != "embedded" || string(m.Content) != "embedded review" {
			t.Fatalf("got %+v", m)
		}
	})

	t.Run("missing", func(t *testing.T) {
		_, ok, err := a.FirstMatch("nope.prompt.md")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if ok {
			t.Fatalf("expected ok=false")
		}
	})
}

// brokenFS returns a non-fs.ErrNotExist error from ReadFile to verify error
// propagation.
type brokenFS struct{}

func (brokenFS) Open(name string) (fs.File, error)     { return nil, errors.New("disk on fire") }
func (brokenFS) ReadFile(name string) ([]byte, error)  { return nil, errors.New("disk on fire") }
func (brokenFS) Glob(pattern string) ([]string, error) { return nil, nil }

func TestFirstMatchPropagatesNonNotExistErrors(t *testing.T) {
	a := New([]Anchor{{Name: "broken", FS: brokenFS{}}})
	_, _, err := a.FirstMatch("anything")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAllMatchesDistinct(t *testing.T) {
	// All distinct files: embedded ships context, org adds org_policy, project adds local.
	// Expected order: embedded first, org next, project last; lex within each.
	anchors := mkAnchors(
		map[string]string{"_pre.local.md": "P-local"},
		map[string]string{"_pre.org_policy.md": "O-policy"},
		map[string]string{"_pre.context.md": "E-context"},
	)
	a := New(anchors)
	got, err := a.AllMatches("_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Anchor: "embedded", Path: "_pre.context.md", Content: []byte("E-context")},
		{Anchor: "org", Path: "_pre.org_policy.md", Content: []byte("O-policy")},
		{Anchor: "project", Path: "_pre.local.md", Content: []byte("P-local")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestAllMatchesOverloadKeepsSlot(t *testing.T) {
	// Embedded: alpha, context, foo. Project overrides context. Expected: alpha
	// (embedded), context (project's content, embedded's slot since that's
	// where it first appears walking most-general-first), foo (embedded).
	anchors := mkAnchors(
		map[string]string{"_pre.context.md": "P-context"},
		nil,
		map[string]string{
			"_pre.alpha.md":   "E-alpha",
			"_pre.context.md": "E-context",
			"_pre.foo.md":     "E-foo",
		},
	)
	a := New(anchors)
	got, err := a.AllMatches("_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Anchor: "embedded", Path: "_pre.alpha.md", Content: []byte("E-alpha")},
		{Anchor: "project", Path: "_pre.context.md", Content: []byte("P-context")},
		{Anchor: "embedded", Path: "_pre.foo.md", Content: []byte("E-foo")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestAllMatchesProjectOnlyAdditions(t *testing.T) {
	// Project adds two files not present in embedded; they should appear after
	// embedded's files in lex order within the project slot.
	anchors := mkAnchors(
		map[string]string{
			"_pre.aaa.md": "P-aaa",
			"_pre.zzz.md": "P-zzz",
		},
		nil,
		map[string]string{"_pre.context.md": "E-context"},
	)
	a := New(anchors)
	got, err := a.AllMatches("_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Anchor: "embedded", Path: "_pre.context.md", Content: []byte("E-context")},
		{Anchor: "project", Path: "_pre.aaa.md", Content: []byte("P-aaa")},
		{Anchor: "project", Path: "_pre.zzz.md", Content: []byte("P-zzz")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestAllMatchesEmpty(t *testing.T) {
	a := New(mkAnchors(nil, nil, nil))
	got, err := a.AllMatches("_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}

func TestAllMatchesNestedGlob(t *testing.T) {
	// fs.Glob supports `dir/*.md` patterns — verify the nested case (the
	// assembler walks `report/_pre.*.md` etc.).
	anchors := mkAnchors(
		map[string]string{"report/_pre.local.md": "P-local"},
		nil,
		map[string]string{
			"report/_pre.intro.md":   "E-intro",
			"report/_post.format.md": "E-format",
		},
	)
	a := New(anchors)
	got, err := a.AllMatches("report/_pre.*.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []Match{
		{Anchor: "embedded", Path: "report/_pre.intro.md", Content: []byte("E-intro")},
		{Anchor: "project", Path: "report/_pre.local.md", Content: []byte("P-local")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestAnchorsReturnsCopy(t *testing.T) {
	original := mkAnchors(nil, nil, nil)
	a := New(original)
	got := a.Anchors()
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	got[0].Name = "mutated"
	if a.Anchors()[0].Name == "mutated" {
		t.Fatal("Anchors() returned internal slice")
	}
}
