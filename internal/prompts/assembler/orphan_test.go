package assembler

import (
	"strings"
	"testing"
)

func TestFindOrphansClean(t *testing.T) {
	// Every fragment has a matching prompt — no orphans.
	anchors := mkAnchors(
		nil, nil,
		map[string]string{
			"report/security.prompt.md":      "main",
			"report/security.pre.scope.md":   "pre",
			"report/security.post.format.md": "post",
			"report/_pre.intro.md":           "dir-pre",
			"_pre.context.md":                "root-pre",
			"review.prompt.md":               "review",
			"review.pre.format.md":           "review pre",
		},
	)
	a := New(anchors)
	orphans, err := a.FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans, got %d: %+v", len(orphans), orphans)
	}
}

func TestFindOrphansDetectsTypo(t *testing.T) {
	// `securty.pre.scope.md` (typo: missing `i`) — no matching prompt.
	anchors := mkAnchors(
		map[string]string{"report/securty.pre.scope.md": "oops"},
		nil,
		map[string]string{"report/security.prompt.md": "main"},
	)
	a := New(anchors)
	orphans, err := a.FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	o := orphans[0]
	if o.Role != "securty" {
		t.Fatalf("role = %q, want securty", o.Role)
	}
	if o.Hint != "security" {
		t.Fatalf("hint = %q, want security", o.Hint)
	}
	if !strings.Contains(o.Error(), "did you mean: security") {
		t.Fatalf("error missing hint: %s", o.Error())
	}
}

func TestFindOrphansAcrossAnchors(t *testing.T) {
	// Prompt in embedded, fragment in project — should NOT be an orphan
	// (matching happens across the full anchor chain).
	anchors := mkAnchors(
		map[string]string{"report/security.pre.local.md": "project fragment"},
		nil,
		map[string]string{"report/security.prompt.md": "embedded main"},
	)
	a := New(anchors)
	orphans, err := a.FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans (cross-anchor match), got %+v", orphans)
	}
}

func TestFindOrphansDifferentDirs(t *testing.T) {
	// `code/security.prompt.md` does NOT satisfy `report/security.pre.scope.md`
	// because the dirs differ.
	anchors := mkAnchors(
		map[string]string{"report/security.pre.scope.md": "orphan"},
		nil,
		map[string]string{"code/security.prompt.md": "wrong dir"},
	)
	a := New(anchors)
	orphans, err := a.FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan, got %d", len(orphans))
	}
	if orphans[0].Dir != "report" {
		t.Fatalf("dir = %q, want report", orphans[0].Dir)
	}
}

func TestFindOrphansNoHintWhenTooFar(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"report/zzzzzz.pre.md": "orphan"},
		nil,
		map[string]string{"report/a.prompt.md": "tiny"},
	)
	a := New(anchors)
	orphans, err := a.FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].Hint != "" {
		t.Fatalf("expected orphan with empty hint, got %+v", orphans)
	}
}

func TestFindOrphansDirLevelIsNotOrphan(t *testing.T) {
	// `_pre.local.md` at root is dir-level, never an orphan even without prompts.
	anchors := mkAnchors(
		map[string]string{"_pre.local.md": "root pre"},
		nil, nil,
	)
	a := New(anchors)
	orphans, err := a.FindOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected 0 orphans, got %+v", orphans)
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"security", "securty", 1},
		{"kitten", "sitting", 3},
		{"", "abc", 3},
		{"abc", "", 3},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
