package agent

import (
	"os"
	"strings"
	"testing"
)

// =============================================================================
// REGRESSION: buildProcessEnv doesn't deduplicate env vars
// File: agent.go, func buildProcessEnv
//
// When agentEnv has a key that already exists in the process environment,
// buildProcessEnv appends the new value WITHOUT removing the old one.
// This produces duplicate entries (e.g. two PATH= lines).
//
// The same issue applies to reqEnv: it always appends, never deduplicates.
//
// With exec.Cmd.Env containing duplicates, behavior is platform-dependent —
// typically the last value wins on Linux, but this is undefined.
// =============================================================================

func TestBuildProcessEnvDuplicatesAgentEnv(t *testing.T) {
	// Set a known env var that we'll also include in agentEnv.
	const testKey = "ATEAM_TEST_BUILD_ENV_KEY"
	const originalValue = "original"
	const overrideValue = "override"

	os.Setenv(testKey, originalValue)
	defer os.Unsetenv(testKey)

	agentEnv := map[string]string{
		testKey: overrideValue,
	}

	result := buildProcessEnv(agentEnv, nil)

	// Count how many times testKey appears.
	count := 0
	for _, entry := range result {
		if strings.HasPrefix(entry, testKey+"=") {
			count++
		}
	}

	if count > 1 {
		t.Errorf("buildProcessEnv produced %d entries for %s — expected exactly 1 (deduplication missing).\n"+
			"Having duplicate env vars causes undefined behavior with exec.Cmd.Env.",
			count, testKey)
	}
	if count == 0 {
		t.Errorf("buildProcessEnv lost %s entirely", testKey)
	}
}

func TestBuildProcessEnvDuplicatesReqEnv(t *testing.T) {
	const testKey = "ATEAM_TEST_REQ_ENV_KEY"
	const originalValue = "original"
	const reqValue = "from-request"

	os.Setenv(testKey, originalValue)
	defer os.Unsetenv(testKey)

	reqEnv := map[string]string{
		testKey: reqValue,
	}

	result := buildProcessEnv(nil, reqEnv)

	count := 0
	var values []string
	for _, entry := range result {
		if strings.HasPrefix(entry, testKey+"=") {
			count++
			values = append(values, entry)
		}
	}

	if count > 1 {
		t.Errorf("buildProcessEnv produced %d entries for %s: %v — expected exactly 1",
			count, testKey, values)
	}
}

func TestBuildProcessEnvTripleDuplicate(t *testing.T) {
	// Worst case: key in parent env, agentEnv, AND reqEnv.
	const testKey = "ATEAM_TEST_TRIPLE_KEY"

	os.Setenv(testKey, "parent")
	defer os.Unsetenv(testKey)

	agentEnv := map[string]string{testKey: "agent"}
	reqEnv := map[string]string{testKey: "request"}

	result := buildProcessEnv(agentEnv, reqEnv)

	count := 0
	for _, entry := range result {
		if strings.HasPrefix(entry, testKey+"=") {
			count++
		}
	}

	if count != 1 {
		t.Errorf("buildProcessEnv produced %d entries for %s — expected exactly 1, "+
			"but parent + agentEnv + reqEnv all contribute a copy",
			count, testKey)
	}
}

// =============================================================================
// REGRESSION: StreamEvent has no CacheWriteTokens field
// File: agent.go, type StreamEvent
//
// The DB schema has cache_write_tokens, but StreamEvent only has
// CacheReadTokens. The claude.go and codex.go agents never populate a
// cache_write field because it doesn't exist on StreamEvent.
// =============================================================================

func TestStreamEventHasCacheWriteTokens(t *testing.T) {
	ev := StreamEvent{
		Type:             "result",
		CacheReadTokens:  500,
		CacheWriteTokens: 300,
	}

	if ev.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", ev.CacheReadTokens)
	}
	if ev.CacheWriteTokens != 300 {
		t.Errorf("CacheWriteTokens = %d, want 300", ev.CacheWriteTokens)
	}
}
