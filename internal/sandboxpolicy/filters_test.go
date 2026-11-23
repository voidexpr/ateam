package sandboxpolicy

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var testFilters = FilterSet{
	"dev/go-write":  "version 1\n/go/pkg\n/{Library/Caches,.cache}/go-build\n",
	"dev/all-write": "version 1\nuse dev/go-write\n/.npm\n",
	"secrets":       "version 1\nroot ~\nexclude /.ssh\nexclude /.config/{gh,gcloud}\n/.ssh/config\n",
	"system":        "version 1\nroot /\n/usr/local/include\n",
	"globby":        "version 1\nexclude /**/*.pem\n",
	"loop-a":        "version 1\nuse loop-b\n",
	"loop-b":        "version 1\nuse loop-a\n",
}

func TestFilterPaths(t *testing.T) {
	inc, exc, err := testFilters.Paths("dev/all-write", "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(inc)
	want := []string{"/home/u/.cache/go-build", "/home/u/.npm", "/home/u/Library/Caches/go-build", "/home/u/go/pkg"}
	if !slices.Equal(inc, want) || len(exc) != 0 {
		t.Errorf("include = %v, exclude = %v", inc, exc)
	}
	inc, exc, err = testFilters.Paths("secrets", "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(exc)
	if !slices.Equal(inc, []string{"/home/u/.ssh/config"}) || !slices.Equal(exc, []string{"/home/u/.config/gcloud", "/home/u/.config/gh", "/home/u/.ssh"}) {
		t.Errorf("include = %v, exclude = %v", inc, exc)
	}
	if inc, _, err := testFilters.Paths("system", "/home/u"); err != nil || !slices.Equal(inc, []string{"/usr/local/include"}) {
		t.Errorf("a root / filter: %v, %v", inc, err)
	}
}

func TestFilterErrors(t *testing.T) {
	for name, want := range map[string]string{
		"nope":   `unknown filter "nope"`,
		"globby": "not one path",
		"loop-a": "use-cycle",
	} {
		_, _, err := testFilters.Paths(name, "/home/u")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want an error containing %q, got %v", name, want, err)
		}
	}
	if err := testFilters.Check(); err == nil {
		t.Error("Check must report the broken filters")
	}
}

func TestBuildResolvesFilterNames(t *testing.T) {
	in := testInput(t)
	in.Filters = testFilters
	in.Write = []string{"dev/go-write", "~/extra"}
	in.Read = []string{"secrets"}
	in.Origin = func(attr, entry string) string { return "myprofile" }
	p, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if w := paths(p.Write); !slices.Contains(w, filepath.Join(in.Home, "go/pkg")) || !slices.Contains(w, filepath.Join(in.Home, "extra")) {
		t.Errorf("write = %v", w)
	}
	if ex := paths(p.Exclude); !slices.Contains(ex, filepath.Join(in.Home, ".ssh")) || !slices.Contains(ex, filepath.Join(in.Home, ".config/gh")) {
		t.Errorf("a read filter's excludes are unreadable: %v", ex)
	}
	if r := paths(p.Read); !slices.Contains(r, filepath.Join(in.Home, ".ssh/config")) {
		t.Errorf("a read filter's include reopens: %v", r)
	}
	if got := p.Write[0].Source; got != "myprofile, filter dev/go-write" {
		t.Errorf("source = %q", got)
	}

	in.Exclude = []string{"dev/go-write"}
	in.Write, in.Read = nil, nil
	p, err = Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths(p.Exclude), filepath.Join(in.Home, "go/pkg")) {
		t.Error("a filter named in exclude excludes the paths it lists")
	}

	in.Exclude = []string{"nope"}
	if _, err := Build(in); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("an unknown filter must be an error, got %v", err)
	}
}
