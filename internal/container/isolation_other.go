//go:build !darwin && !linux

package container

// detectOSSandbox is a no-op on platforms without a supported probe.
// Explicit env-var signals (ATEAM_IN_SANDBOX, FENCE_SANDBOX, etc.)
// still work via detectIsolation's earlier branches.
func detectOSSandbox() string { return "" }
