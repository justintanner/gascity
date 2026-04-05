package main

import "testing"

// gcEnvVars lists the GC_* identity and session-routing variables that
// tests should clear to isolate from host session state (e.g., running
// inside a gc-managed tmux session).
var gcEnvVars = []string{
	"GC_ALIAS",
	"GC_AGENT",
	"GC_SESSION_ID",
	"GC_SESSION_NAME",
	"GC_TMUX_SESSION",
	"GC_CITY",
}

// clearGCEnv clears GC_* identity and session-routing variables for the
// duration of the test, preventing host session state from leaking into
// tests. Uses t.Setenv so values are automatically restored.
func clearGCEnv(t *testing.T) {
	t.Helper()
	for _, k := range gcEnvVars {
		t.Setenv(k, "")
	}
}
