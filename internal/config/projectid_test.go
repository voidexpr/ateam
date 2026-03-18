package config

import (
	"strings"
	"testing"
)

func TestPathToProjectID_Escaping(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"myproject", "myproject"},
		{"foo/bar", "foo_bar"},
		{"foo_bar", "foo__bar"},
		{"a/b/c", "a_b_c"},
		{"", ""},
		{"services/api", "services_api"},
		{"my_project/sub_dir", "my__project_sub__dir"},
	}
	for _, tc := range cases {
		got := PathToProjectID(tc.path)
		if got != tc.want {
			t.Errorf("PathToProjectID(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestPathToProjectID_NoCollision(t *testing.T) {
	a := PathToProjectID("foo/bar")
	b := PathToProjectID("foo_bar")
	if a == b {
		t.Errorf("collision: foo/bar and foo_bar both produce %q", a)
	}
}

func TestPathToProjectID_LongPath(t *testing.T) {
	long := strings.Repeat("a/", 200) + "end"
	key := PathToProjectID(long)
	if len(key) > maxProjectIDLen {
		t.Errorf("key length %d exceeds max %d", len(key), maxProjectIDLen)
	}
	if !strings.Contains(key, "_") {
		t.Error("expected hash suffix in truncated key")
	}
}

func TestProjectIDToPath(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"myproject", "myproject"},
		{"foo_bar", "foo/bar"},
		{"foo__bar", "foo_bar"},
		{"a_b_c", "a/b/c"},
		{"", ""},
		{"services_api", "services/api"},
		{"my__project_sub__dir", "my_project/sub_dir"},
	}
	for _, tc := range cases {
		got := ProjectIDToPath(tc.id)
		if got != tc.want {
			t.Errorf("ProjectIDToPath(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestProjectIDRoundTrip(t *testing.T) {
	paths := []string{"myproject", "foo/bar", "foo_bar", "a/b/c", "services/api", "my_project/sub_dir"}
	for _, p := range paths {
		id := PathToProjectID(p)
		got := ProjectIDToPath(id)
		if got != p {
			t.Errorf("round-trip failed: %q -> %q -> %q", p, id, got)
		}
	}
}

func TestValidateProjectPath(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"myproject", false},
		{"foo/bar", false},
		{"a/b/c", false},
		{"foo_bar", false},
		{".", true},
		{"..", true},
		{"./relative", true},
		{"foo/./bar", true},
		{"foo/../bar", true},
	}
	for _, tc := range cases {
		err := ValidateProjectPath(tc.path)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateProjectPath(%q) error = %v, wantErr %v", tc.path, err, tc.wantErr)
		}
	}
}
