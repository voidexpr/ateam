package prompts

import "testing"

// TestDefaultPromptFactory_ReturnsPromptText pins the shipped default:
// every path produces a PromptText wrapping the file body. Future
// factories that branch on extension fall through to this for unknown
// shapes, so the contract is "always returns a non-nil PromptText".
func TestDefaultPromptFactory_ReturnsPromptText(t *testing.T) {
	fac := DefaultPromptFactory()
	cases := []string{
		"foo.prompt.md",
		"dir/foo.prompt.md",
		"foo.prompt.py", // future Python impl — default still answers
		"anything.txt",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			got := fac.For(path, "BODY")
			pt, ok := got.(PromptText)
			if !ok {
				t.Fatalf("got %T, want PromptText", got)
			}
			if pt.Text != "BODY" {
				t.Errorf("Text=%q, want BODY", pt.Text)
			}
		})
	}
}
