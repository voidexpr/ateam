package runtime

import (
	"reflect"
	"testing"
)

func TestDetectStrayAllCaps(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "all known tokens — clean",
			in: `docker_container = "{{CONTAINER_NAME}}"
args = ["--exec-id", "{{EXEC_ID}}", "--batch", "{{BATCH}}"]`,
			want: nil,
		},
		{
			name: "typo — single stray",
			in:   `docker_container = "{{CONTAINR_NAME}}"`,
			want: []string{"CONTAINR_NAME"},
		},
		{
			name: "multiple stray tokens — deduped and sorted",
			in: `a = "{{FOO}} {{BAR}}"
b = "{{FOO}}"
c = "{{ROLE}}"`,
			want: []string{"BAR", "FOO"},
		},
		{
			name: "dotted form ignored",
			in:   `desc = "{{prompt.name}} {{exec.id}}"`,
			want: nil,
		},
		{
			name: "directive with whitespace ignored (not bare token)",
			in:   `body = "{{include _pre.context.md}}"`,
			want: nil,
		},
		{
			name: "mixed known + stray",
			in:   `cmd = "{{EXEC_ID}} {{MYTOOL}}"`,
			want: []string{"MYTOOL"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectStrayAllCaps([]byte(tc.in))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
