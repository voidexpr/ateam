package agent

import (
	"strings"
	"testing"
)

// =============================================================================
// REGRESSION: buildProcessEnv must deduplicate env vars
// File: agent.go, func buildProcessEnv
//
// When agentEnv or reqEnv has a key that already exists in the process
// environment, buildProcessEnv must replace the existing entry rather than
// append a second one: exec.Cmd.Env with duplicates is platform-dependent.
// Precedence is request > agent > parent.
// =============================================================================

// envValues returns every value of key in env, in order.
func envValues(env []string, key string) []string {
	var values []string
	for _, entry := range env {
		if v, ok := strings.CutPrefix(entry, key+"="); ok {
			values = append(values, v)
		}
	}
	return values
}

func assertSingleEnvValue(t *testing.T, env []string, key, want string) {
	t.Helper()
	values := envValues(env, key)
	if len(values) != 1 {
		t.Fatalf("buildProcessEnv produced %d entries for %s: %v — expected exactly 1", len(values), key, values)
	}
	if values[0] != want {
		t.Errorf("%s = %q, want %q", key, values[0], want)
	}
}

func TestBuildProcessEnvDuplicatesAgentEnv(t *testing.T) {
	const testKey = "ATEAM_TEST_BUILD_ENV_KEY"
	t.Setenv(testKey, "original")

	result := buildProcessEnv(map[string]string{testKey: "override"}, nil)

	assertSingleEnvValue(t, result, testKey, "override")
}

func TestBuildProcessEnvDuplicatesReqEnv(t *testing.T) {
	const testKey = "ATEAM_TEST_REQ_ENV_KEY"
	t.Setenv(testKey, "original")

	result := buildProcessEnv(nil, map[string]string{testKey: "from-request"})

	assertSingleEnvValue(t, result, testKey, "from-request")
}

func TestBuildProcessEnvTripleDuplicate(t *testing.T) {
	// Worst case: key in parent env, agentEnv, AND reqEnv.
	const testKey = "ATEAM_TEST_TRIPLE_KEY"
	t.Setenv(testKey, "parent")

	agentEnv := map[string]string{testKey: "agent"}
	reqEnv := map[string]string{testKey: "request"}

	result := buildProcessEnv(agentEnv, reqEnv)

	assertSingleEnvValue(t, result, testKey, "request")
}
