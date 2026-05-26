package assembler

import (
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantMeta Frontmatter
		wantBody string
	}{
		{
			"no frontmatter",
			"just body content",
			Frontmatter{},
			"just body content",
		},
		{
			"description only",
			"---\ndescription: hello\n---\nbody here\n",
			Frontmatter{Description: "hello"},
			"body here\n",
		},
		{
			"all three keys",
			"---\ndescription: test role\ndeprecated: true\nlegacy: false\n---\nbody\n",
			Frontmatter{Description: "test role", Deprecated: true},
			"body\n",
		},
		{
			"quoted value",
			"---\ndescription: \"quoted text\"\n---\nbody",
			Frontmatter{Description: "quoted text"},
			"body",
		},
		{
			"comment line tolerated",
			"---\n# this is a comment\ndescription: x\n---\nbody",
			Frontmatter{Description: "x"},
			"body",
		},
		{
			"empty line in block tolerated",
			"---\ndescription: x\n\nlegacy: true\n---\nbody",
			Frontmatter{Description: "x", Legacy: true},
			"body",
		},
		{
			"strips leading body newlines",
			"---\ndescription: x\n---\n\n\nbody",
			Frontmatter{Description: "x"},
			"body",
		},
		{
			"empty body",
			"---\ndescription: x\n---\n",
			Frontmatter{Description: "x"},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta, body, err := ParseFrontmatter(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if meta != tc.wantMeta {
				t.Fatalf("meta = %+v, want %+v", meta, tc.wantMeta)
			}
			if body != tc.wantBody {
				t.Fatalf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestParseFrontmatterErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		sub  string
	}{
		{
			"unknown key",
			"---\nfoo: bar\n---\nbody",
			"unknown key",
		},
		{
			"missing close",
			"---\ndescription: x\nbody never closes",
			"no closing",
		},
		{
			"bad bool",
			"---\ndeprecated: maybe\n---\nbody",
			"invalid bool",
		},
		{
			"non kv line",
			"---\nthis is not yaml\n---\nbody",
			"expected `key: value`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseFrontmatter(tc.in)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.sub) {
				t.Fatalf("error = %v, want substring %q", err, tc.sub)
			}
		})
	}
}
