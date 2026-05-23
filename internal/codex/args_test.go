package codex

import (
	"slices"
	"testing"
)

func TestInteractiveArgs(t *testing.T) {
	got := InteractiveArgs(
		[]string{"--no-alt-screen", "--sandbox", "workspace-write"},
		"gpt-5.5",
		"xhigh",
		[]string{"--ask-for-approval", "never"},
	)
	want := []string{
		"--no-alt-screen",
		"--sandbox", "workspace-write",
		"-c", "check_for_update_on_startup=false",
		"--disable", "apps",
		"--disable", "plugins",
		"--model", "gpt-5.5",
		"-c", "model_reasoning_effort=xhigh",
		"--ask-for-approval", "never",
	}
	if !slices.Equal(got, want) {
		t.Errorf("InteractiveArgs = %v, want %v", got, want)
	}
}

func TestInteractiveArgsAddsUnattendedDefaults(t *testing.T) {
	got := InteractiveArgs(nil, "", "", nil)
	want := []string{
		"--no-alt-screen",
		"-s", "workspace-write",
		"-a", "never",
		"-c", "check_for_update_on_startup=false",
		"--disable", "apps",
		"--disable", "plugins",
	}
	if !slices.Equal(got, want) {
		t.Errorf("InteractiveArgs = %v, want %v", got, want)
	}
}

func TestInteractiveArgsHonorsExplicitUnattendedOverrides(t *testing.T) {
	got := InteractiveArgs([]string{"-c", "check_for_update_on_startup=true"}, "", "", []string{"--enable", "apps", "-c", "features.plugins=true"})
	want := []string{
		"-c", "check_for_update_on_startup=true",
		"--no-alt-screen",
		"-s", "workspace-write",
		"-a", "never",
		"--enable", "apps",
		"-c", "features.plugins=true",
	}
	if !slices.Equal(got, want) {
		t.Errorf("InteractiveArgs = %v, want %v", got, want)
	}
}
