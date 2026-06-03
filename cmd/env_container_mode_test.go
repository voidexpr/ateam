package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/ateam/internal/container"
)

// TestPrintContainerModeMatrix exercises the four corners of the
// detection matrix the user sees in `ateam env`, end-to-end through
// the helper that renders it. Each case checks:
//
//   - the "Agent in container mode:" headline (true/false + via tag)
//   - the per-layer "detected: yes|no [(source)] — toggle=… , [NOT] applied" row
//
// Reads/writes via env vars + the container package's setters, which is
// the same wiring real CLI flags + runtime.hcl resolve into.
func TestPrintContainerModeMatrix(t *testing.T) {
	// Snapshot env vars so we can mutate them per-case via t.Setenv on
	// the subtest, then reset detection state in Cleanup so other
	// cmd tests aren't affected.
	t.Cleanup(container.ResetDetectionForTest)

	type assertion struct {
		mustContain    []string
		mustNotContain []string
	}

	cases := []struct {
		name           string
		envVars        map[string]string
		setup          func()
		skipIfHostInOS bool // skip on macOS Seatbelt / Linux bwrap hosts (OS probe is a true positive there, can't observe a "clean host" outcome)
		check          assertion
	}{
		{
			name:           "clean host, defaults",
			envVars:        map[string]string{},
			setup:          func() {},
			skipIfHostInOS: true,
			check: assertion{
				mustContain: []string{
					"Agent in container mode: false",
					"Docker  detected: no",
					"docker_detection=true",
					"sandbox_detection=false",
				},
				mustNotContain: []string{
					"NOT applied",
					"applied", // the verb only appears when something was detected
				},
			},
		},
		{
			name:    "fence detected, sandbox_detection off (default)",
			envVars: map[string]string{"FENCE_SANDBOX": "1"},
			setup:   func() {},
			check: assertion{
				mustContain: []string{
					"Agent in container mode: false",
					"Sandbox detected: yes (fence)",
					"sandbox_detection=false, NOT applied",
				},
				mustNotContain: []string{
					// We deliberately stopped prescribing the user's choice;
					// fail if the hint comes back.
					"set to true to apply",
				},
			},
		},
		{
			name:    "fence detected, sandbox_detection on -> in container mode (via sandbox)",
			envVars: map[string]string{"FENCE_SANDBOX": "1"},
			setup: func() {
				container.SetSandboxDetection(true)
			},
			check: assertion{
				mustContain: []string{
					"Agent in container mode: true (via sandbox)",
					"Sandbox detected: yes (fence) — sandbox_detection=true, applied",
				},
				mustNotContain: []string{
					"NOT applied",
				},
			},
		},
		{
			name:    "docker_detection off, headline reflects toggle in matrix",
			envVars: map[string]string{},
			setup: func() {
				container.SetDockerDetection(false)
			},
			check: assertion{
				mustContain: []string{
					"Docker  detected: no — docker_detection=false",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset between subtests so toggles + sync.Once cache
			// don't bleed across cases.
			container.ResetDetectionForTest()

			// Clear every env var the detector reads, then set just
			// the ones this case wants.
			for _, k := range []string{
				"ATEAM_IN_CONTAINER", "ATEAM_IN_SANDBOX",
				"FENCE_SANDBOX", "FIREJAIL_NAME", "container",
			} {
				_ = os.Unsetenv(k)
			}
			for k, v := range tc.envVars {
				t.Setenv(k, v)
			}

			if tc.skipIfHostInOS {
				if yes, src := container.IsInSandbox(); yes {
					t.Skipf("host itself is in an outer sandbox (%s); cannot observe a clean-host outcome", src)
				}
				if m := container.DockerMarker(); m != "" {
					t.Skipf("host itself is in a Docker container (%s); cannot observe a clean-host outcome", m)
				}
			}

			tc.setup()

			out := captureStdout(t, printContainerMode)

			for _, want := range tc.check.mustContain {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\n---\n%s", want, out)
				}
			}
			for _, banned := range tc.check.mustNotContain {
				if strings.Contains(out, banned) {
					t.Errorf("output should not contain %q\n---\n%s", banned, out)
				}
			}
		})
	}
}

// TestParseBoolFlagAcceptsStrictValues exercises the root-command
// flag parser that backs --sandbox-detection / --docker-detection.
// Only 'true' / 'false' are accepted; everything else errors. This is
// the user-visible contract change from the old bool-flag shape.
func TestParseBoolFlagAcceptsStrictValues(t *testing.T) {
	cases := []struct {
		in       string
		want     bool
		wantErr  bool
		errMatch string
	}{
		{in: "true", want: true},
		{in: "false", want: false},
		{in: "True", wantErr: true, errMatch: "requires 'true' or 'false'"},
		{in: "1", wantErr: true, errMatch: "requires 'true' or 'false'"},
		{in: "yes", wantErr: true, errMatch: "requires 'true' or 'false'"},
		{in: "", wantErr: true, errMatch: "requires 'true' or 'false'"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseBoolFlag("sandbox-detection", tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error for %q, got nil", tc.in)
				}
				if !strings.Contains(err.Error(), tc.errMatch) {
					t.Fatalf("want error containing %q, got %v", tc.errMatch, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
