package config

import (
	"strings"
	"testing"
)

func TestPathToStateKey_Roundtrip(t *testing.T) {
	cases := []struct {
		path string
	}{
		{"myproject"},
		{"level1/myproj"},
		{"a/b/c/d"},
		{"."},
		{"foo_bar"},
		{"foo__bar"},
		{"my_project/sub_dir"},
		{"services/api"},
		{".."},
		{"./relative"},
	}
	for _, tc := range cases {
		key := PathToStateKey(tc.path)
		got := StateKeyToPath(key)
		if got != tc.path {
			t.Errorf("roundtrip(%q): key=%q, back=%q", tc.path, key, got)
		}
	}
}

func TestPathToStateKey_Escaping(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{".", "_D"},
		{"myproject", "myproject"},
		{"foo/bar", "foo_Sbar"},
		{"foo_bar", "foo__bar"},
		{"a/b/c", "a_Sb_Sc"},
		{"..", "_D_D"},
		{"", ""},
	}
	for _, tc := range cases {
		got := PathToStateKey(tc.path)
		if got != tc.want {
			t.Errorf("PathToStateKey(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestPathToStateKey_NoCollision(t *testing.T) {
	// foo/bar and foo_bar must produce different keys
	a := PathToStateKey("foo/bar")
	b := PathToStateKey("foo_bar")
	if a == b {
		t.Errorf("collision: foo/bar and foo_bar both produce %q", a)
	}
}

func TestPathToStateKey_LongPath(t *testing.T) {
	long := strings.Repeat("a/", 200) + "end"
	key := PathToStateKey(long)
	if len(key) > maxStateKeyLen {
		t.Errorf("key length %d exceeds max %d", len(key), maxStateKeyLen)
	}
	// Truncated keys contain a hash suffix starting with _
	if !strings.Contains(key, "_") {
		t.Error("expected hash suffix in truncated key")
	}
}

func TestStateKeyToPath_Empty(t *testing.T) {
	if got := StateKeyToPath(""); got != "" {
		t.Errorf("StateKeyToPath(\"\") = %q, want \"\"", got)
	}
}
