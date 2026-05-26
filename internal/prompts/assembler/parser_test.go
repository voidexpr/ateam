package assembler

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Parsed
	}{
		// Role main
		{"role main", "security.prompt.md", Parsed{Kind: KindRoleMain, Role: "security"}},
		{"dotted role main", "code.bugs.prompt.md", Parsed{Kind: KindRoleMain, Role: "code.bugs"}},
		{"singleton at root", "review.prompt.md", Parsed{Kind: KindRoleMain, Role: "review"}},

		// Role pre/post singleton
		{"role pre singleton", "security.pre.md", Parsed{Kind: KindRolePre, Role: "security"}},
		{"role post singleton", "security.post.md", Parsed{Kind: KindRolePost, Role: "security"}},
		{"dotted role pre", "code.bugs.pre.md", Parsed{Kind: KindRolePre, Role: "code.bugs"}},

		// Role pre/post fragment
		{"role pre fragment", "security.pre.scope.md", Parsed{Kind: KindRolePre, Role: "security", Fragment: "scope"}},
		{"role post fragment", "security.post.format.md", Parsed{Kind: KindRolePost, Role: "security", Fragment: "format"}},
		{"dotted role + fragment", "code.bugs.pre.scope.md", Parsed{Kind: KindRolePre, Role: "code.bugs", Fragment: "scope"}},
		{"fragment with dots", "security.pre.scope.detail.md", Parsed{Kind: KindRolePre, Role: "security", Fragment: "scope.detail"}},

		// Dir-level
		{"dir pre singleton", "_pre.md", Parsed{Kind: KindDirPre}},
		{"dir post singleton", "_post.md", Parsed{Kind: KindDirPost}},
		{"dir pre fragment", "_pre.context.md", Parsed{Kind: KindDirPre, Fragment: "context"}},
		{"dir post fragment", "_post.format.md", Parsed{Kind: KindDirPost, Fragment: "format"}},
		{"dir pre fragment dotted name", "_pre.org.policy.md", Parsed{Kind: KindDirPre, Fragment: "org.policy"}},

		// Non-marker substrings that look similar
		{"pretty.md is unknown", "pretty.md", Parsed{Kind: KindUnknown}},
		{"preamble in name", "code.preamble.md", Parsed{Kind: KindUnknown}},
		{"prefix word", "code.prefix.md", Parsed{Kind: KindUnknown}},
		{"posting word", "code.posting.md", Parsed{Kind: KindUnknown}},

		// Walks left to find a real marker behind a fake one
		{"fake-pre then real pre", "code.preamble.pre.md", Parsed{Kind: KindRolePre, Role: "code.preamble"}},
		{"real pre then word", "code.pre.amble.md", Parsed{Kind: KindRolePre, Role: "code", Fragment: "amble"}},

		// Arbitrary includes
		{"plain include", "notes.md", Parsed{Kind: KindUnknown}},
		{"deeply named include", "foo.bar.baz.md", Parsed{Kind: KindUnknown}},

		// Edge / malformed
		{"empty body", ".md", Parsed{Kind: KindUnknown}},
		{"underscore only", "_.md", Parsed{Kind: KindUnknown}},
		{"underscore pre. trailing empty", "_pre..md", Parsed{Kind: KindUnknown}},
		{"only role main marker", ".prompt.md", Parsed{Kind: KindUnknown}},
		{"non-md", "security.prompt.txt", Parsed{Kind: KindUnknown}},
		{"empty string", "", Parsed{Kind: KindUnknown}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.in)
			if got != tc.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}
}
