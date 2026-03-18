//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/test/tmuxtest"
)

// TestTutorialRegression exercises the full Getting Started tutorial flow:
//
//	gc init → gc rig add → bd create (from rig) → gc sling (from rig) → bead closes
//
// This is the exact sequence from the tutorial documentation. It uses real
// Claude inference (requires ANTHROPIC_API_KEY) and tmux sessions.
//
// Regressions caught by this test:
//   - gc init scaffold + supervisor startup
//   - gc rig add beads initialization (set -e landmine in gc-beads-bd)
//   - bd create prefix routing from inside rig directory (hw- not gc-)
//   - gc sling store resolution from bead prefix (cross-rig lookup)
//   - gc sling bare agent name → rig-scoped implicit agent from CWD
//   - Default formula (mol-do-work) instantiation
//   - Agent session lifecycle (start → claim → execute → close)
func TestTutorialRegression(t *testing.T) {
	if usingSubprocess() {
		t.Skip("tutorial regression requires tmux")
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set — skipping inference test")
	}

	guard := tmuxtest.NewGuard(t)
	cityName := guard.CityName()
	cityDir := filepath.Join(t.TempDir(), cityName)

	// ── Phase 1: gc init ────────────────────────────────────────────
	out, err := gc("", "init", "--provider", "claude", "--skip-provider-readiness", cityDir)
	if err != nil {
		t.Fatalf("gc init failed: %v\n%s", err, out)
	}
	t.Logf("gc init:\n%s", out)

	// Verify city.toml content.
	toml := readFile(t, filepath.Join(cityDir, "city.toml"))
	assertContains(t, toml, `provider = "claude"`, "city.toml missing provider")
	assertContains(t, toml, `name = "mayor"`, "city.toml missing mayor agent")
	if strings.Contains(toml, "[api]") {
		t.Errorf("city.toml has spurious [api] section (regression)")
	}

	// ── Phase 2: gc start ───────────────────────────────────────────
	out, err = gc("", "start", cityDir)
	if err != nil {
		t.Fatalf("gc start failed: %v\n%s", err, out)
	}
	t.Logf("gc start:\n%s", out)
	t.Cleanup(func() { gc("", "stop", cityDir) }) //nolint:errcheck

	// Give supervisor a moment to reconcile.
	time.Sleep(2 * time.Second)

	// ── Phase 3: gc rig add ─────────────────────────────────────────
	rigDir := filepath.Join(cityDir, "rigs", "hello-world")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("creating rig dir: %v", err)
	}

	out, err = gc(cityDir, "rig", "add", "rigs/hello-world")
	if err != nil {
		t.Fatalf("gc rig add failed: %v\n%s", err, out)
	}
	t.Logf("gc rig add:\n%s", out)

	// Regression: set -e bug prevented .beads/ creation.
	beadsDir := filepath.Join(rigDir, ".beads")
	if _, serr := os.Stat(beadsDir); os.IsNotExist(serr) {
		t.Fatalf("rig .beads/ not created — gc rig add failed to initialize beads (set -e regression)")
	}

	// Verify city.toml updated with rig.
	toml = readFile(t, filepath.Join(cityDir, "city.toml"))
	assertContains(t, toml, "hello-world", "city.toml missing rig after gc rig add")

	// ── Phase 4: bd create from inside rig ──────────────────────────
	out, err = bd(rigDir, "create", "Write hello world in the language of your choice")
	if err != nil {
		t.Fatalf("bd create failed: %v\n%s", err, out)
	}
	t.Logf("bd create:\n%s", out)

	beadID := extractBeadID(t, out)
	t.Logf("bead ID: %s", beadID)

	// Regression: missing .beads/ caused bd to walk up to city store with gc- prefix.
	if !strings.HasPrefix(beadID, "hw-") {
		t.Fatalf("bead ID %q has wrong prefix — expected hw- (rig prefix)", beadID)
	}

	// ── Phase 5: gc sling from inside rig ───────────────────────────
	// Bare "claude" should resolve to hello-world/claude via CWD context.
	out, err = gc(rigDir, "sling", "claude", beadID)
	if err != nil {
		t.Logf("bare 'claude' sling failed: %v\n%s", err, out)
		// Fallback to fully qualified — if this also fails, it's a real error.
		out, err = gc(rigDir, "sling", "hello-world/claude", beadID)
		if err != nil {
			t.Fatalf("gc sling failed: %v\n%s", err, out)
		}
		t.Errorf("bare 'claude' failed but fully qualified worked — CWD agent resolution regression")
	}
	t.Logf("gc sling:\n%s", out)
	assertContains(t, out, "Slung", "gc sling output missing confirmation")

	// ── Phase 6: wait for bead to close ─────────────────────────────
	deadline := time.Now().Add(5 * time.Minute)
	var lastStatus string
	for time.Now().Before(deadline) {
		out, err = bd(rigDir, "show", beadID)
		if err != nil {
			t.Logf("bd show error (retrying): %v", err)
			time.Sleep(5 * time.Second)
			continue
		}

		lower := strings.ToLower(out)
		if strings.Contains(lower, "closed") {
			t.Logf("bead %s closed", beadID)
			goto closed
		}

		status := parseStatus(lower)
		if status != lastStatus {
			t.Logf("bead %s: %s", beadID, status)
			lastStatus = status
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("bead %s did not close within 5 minutes:\n%s", beadID, out)

closed:
	// ── Phase 7: verify agent output ────────────────────────────────
	entries, err := os.ReadDir(rigDir)
	if err != nil {
		t.Fatalf("reading rig dir: %v", err)
	}
	var produced []string
	for _, e := range entries {
		switch e.Name() {
		case ".beads", ".gitignore", ".git":
			continue
		}
		produced = append(produced, e.Name())
	}
	if len(produced) == 0 {
		t.Errorf("agent closed bead but produced no files in rig dir")
	} else {
		t.Logf("agent produced: %v", produced)
	}
}

// readFile reads a file and returns its content as a string.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// assertContains fails the test if s does not contain substr.
func assertContains(t *testing.T, s, substr, msg string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("%s: %q not found in:\n%s", msg, substr, s)
	}
}

// parseStatus extracts a human-readable status from bd show output.
func parseStatus(lower string) string {
	for _, s := range []string{"closed", "in_progress", "in-progress", "open"} {
		if strings.Contains(lower, s) {
			return s
		}
	}
	return "unknown"
}
