package cmd

import (
	"testing"
)

func TestSetEnvOverwritesNonEmptyValue(t *testing.T) {
	env := []string{"OTHER=x", "KEY=oldvalue", "ANOTHER=y"}
	got := setEnv(env, "KEY", "newvalue")
	want := "KEY=newvalue"
	for _, e := range got {
		if e == "KEY=oldvalue" {
			t.Errorf("setEnv did not replace KEY=oldvalue; env = %v", got)
			return
		}
	}
	found := false
	for _, e := range got {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Errorf("setEnv: want %q in result, got %v", want, got)
	}
	if len(got) != 3 {
		t.Errorf("setEnv: want 3 entries, got %d: %v", len(got), got)
	}
}

func TestSetEnvOverwritesEmptyValue(t *testing.T) {
	// Bug case: KEY= (empty value) must be overwritten, not duplicated.
	env := []string{"OTHER=x", "KEY=", "ANOTHER=y"}
	got := setEnv(env, "KEY", "newvalue")
	want := "KEY=newvalue"
	for _, e := range got {
		if e == "KEY=" {
			t.Errorf("setEnv left old KEY= in env; result = %v", got)
			return
		}
	}
	found := false
	for _, e := range got {
		if e == want {
			found = true
		}
	}
	if !found {
		t.Errorf("setEnv: want %q in result, got %v", want, got)
	}
	if len(got) != 3 {
		t.Errorf("setEnv: want 3 entries (no duplicate), got %d: %v", len(got), got)
	}
}

func TestUnsetEnvRemovesEmptyValue(t *testing.T) {
	// Bug case: KEY= (empty value) must be removed by unsetEnv.
	env := []string{"OTHER=x", "KEY=", "ANOTHER=y"}
	got := unsetEnv(env, "KEY")
	for _, e := range got {
		if e == "KEY=" {
			t.Errorf("unsetEnv did not remove KEY=; result = %v", got)
			return
		}
	}
	if len(got) != 2 {
		t.Errorf("unsetEnv: want 2 entries, got %d: %v", len(got), got)
	}
}
