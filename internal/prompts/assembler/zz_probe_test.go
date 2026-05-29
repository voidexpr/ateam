package assembler

import "testing"

func TestProbeEmptyRoleMainShadowsFallback(t *testing.T) {
	anchors := mkAnchors(
		map[string]string{"report/security.prompt.md": "   \n"}, // project: empty/whitespace
		nil,
		map[string]string{"report/security.prompt.md": "REAL ROLE BODY"}, // embedded: real
	)
	a := New(anchors)
	res, err := a.Assemble("report/security", mkVars(), nil, nil)
	if err != nil {
		t.Fatalf("Assemble err: %v", err)
	}
	t.Logf("Prompt=%q Sections=%d", res.Prompt, len(res.Sections))
	if res.Prompt == "" {
		t.Errorf("REGRESSION CONFIRMED: empty project override suppressed role body -> empty prompt (no error)")
	}
}
