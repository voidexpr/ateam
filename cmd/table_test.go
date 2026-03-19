package cmd

import (
	"path/filepath"
	"testing"

	"github.com/ateam/internal/root"
)

func TestResolveStreamPath(t *testing.T) {
	env := &root.ResolvedEnv{
		OrgDir:     "/home/user/.ateamorg",
		ProjectDir: "/home/user/myproject/.ateam",
	}

	tests := []struct {
		name string
		env  *root.ResolvedEnv
		sf   string
		want string
	}{
		{
			name: "empty string",
			env:  env,
			sf:   "",
			want: "",
		},
		{
			name: "absolute path returned as-is",
			env:  env,
			sf:   "/var/log/stream.jsonl",
			want: "/var/log/stream.jsonl",
		},
		{
			name: "legacy projects/ prefix resolves to orgDir",
			env:  env,
			sf:   "projects/myproject/roles/security/logs/stream.jsonl",
			want: filepath.Join("/home/user/.ateamorg", "projects/myproject/roles/security/logs/stream.jsonl"),
		},
		{
			name: "new relative path resolves to projectDir",
			env:  env,
			sf:   "logs/roles/security/2026-03-18_stream.jsonl",
			want: filepath.Join("/home/user/myproject/.ateam", "logs/roles/security/2026-03-18_stream.jsonl"),
		},
		{
			name: "legacy prefix but empty orgDir falls back to projectDir",
			env: &root.ResolvedEnv{
				OrgDir:     "",
				ProjectDir: "/home/user/myproject/.ateam",
			},
			sf:   "projects/myproject/roles/security/logs/stream.jsonl",
			want: filepath.Join("/home/user/myproject/.ateam", "projects/myproject/roles/security/logs/stream.jsonl"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveStreamPath(tt.env, tt.sf)
			if got != tt.want {
				t.Errorf("resolveStreamPath(%q) = %q, want %q", tt.sf, got, tt.want)
			}
		})
	}
}
