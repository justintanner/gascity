//go:build integration && acceptance

package integration

import (
	"os"
	"os/exec"
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

	guard := tmuxtest.NewGuard(t)
	cityName := guard.CityName()
	cityDir := filepath.Join(t.TempDir(), cityName)

	// ── Phase 1: gc init ────────────────────────────────────────────
	out, err := gcReal("", "init", "--provider", "claude", "--skip-provider-readiness", cityDir)
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
	out, err = gcReal("", "start", cityDir)
	if err != nil {
		t.Fatalf("gc start failed: %v\n%s", err, out)
	}
	t.Logf("gc start:\n%s", out)
	t.Cleanup(func() { gcReal("", "stop", cityDir) }) //nolint:errcheck

	// Give supervisor a moment to reconcile.
	time.Sleep(2 * time.Second)

	// ── Phase 3: gc rig add ─────────────────────────────────────────
	rigDir := filepath.Join(cityDir, "rigs", "hello-world")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatalf("creating rig dir: %v", err)
	}

	out, err = gcReal(cityDir, "rig", "add", "rigs/hello-world")
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
	out, err = bdReal(rigDir, "create", "Write hello world in the language of your choice")
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
	// Bare "claude" from inside the rig dir should resolve to hello-world/claude
	// (rig-scoped implicit agent), not the city-scoped claude.
	out, err = gcReal(rigDir, "sling", "claude", beadID)
	if err != nil {
		t.Fatalf("gc sling claude failed: %v\n%s", err, out)
	}
	t.Logf("gc sling:\n%s", out)
	assertContains(t, out, "Slung", "gc sling output missing confirmation")

	// ── Phase 6: wait for bead to close ─────────────────────────────
	waitForBeadClose(t, rigDir, beadID, 5*time.Minute)

	// ── Phase 7: verify agent output ────────────────────────────────
	produced := listUserFiles(t, rigDir)
	if len(produced) == 0 {
		t.Errorf("agent closed bead but produced no files in rig dir")
	} else {
		t.Logf("agent produced: %v", produced)
	}

	// ── Phase 8: inline text sling ──────────────────────────────────
	// Tutorial: `gc sling claude "Write hello-world.cpp"`
	// Auto-creates a bead from the text and slings it in one command.
	out, err = gcReal(rigDir, "sling", "claude", "Write hello-world.cpp")
	if err != nil {
		t.Fatalf("gc sling inline text failed: %v\n%s", err, out)
	}
	t.Logf("gc sling inline:\n%s", out)
	assertContains(t, out, "Slung", "inline sling output missing confirmation")

	// Extract the auto-created bead ID.
	inlineBeadID := extractSlingBeadID(t, out)
	t.Logf("inline bead ID: %s", inlineBeadID)

	if !strings.HasPrefix(inlineBeadID, "hw-") {
		t.Errorf("inline bead ID %q has wrong prefix — expected hw-", inlineBeadID)
	}

	// Wait for the inline bead to close.
	waitForBeadClose(t, rigDir, inlineBeadID, 5*time.Minute)

	// Verify hello-world.cpp was created.
	cppPath := filepath.Join(rigDir, "hello-world.cpp")
	if _, serr := os.Stat(cppPath); os.IsNotExist(serr) {
		allFiles := listUserFiles(t, rigDir)
		var cppFiles []string
		for _, f := range allFiles {
			if strings.HasSuffix(f, ".cpp") {
				cppFiles = append(cppFiles, f)
			}
		}
		if len(cppFiles) == 0 {
			t.Errorf("agent did not produce any .cpp file; files: %v", allFiles)
		} else {
			t.Logf("agent produced .cpp files: %v (expected hello-world.cpp)", cppFiles)
		}
	} else {
		t.Logf("hello-world.cpp created successfully")
	}
}

// acceptanceEnv returns the integration env but WITHOUT GC_DOLT=skip,
// so bd and gc can use the real dolt server started by the supervisor.
func acceptanceEnv() []string {
	env := integrationEnv()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "GC_DOLT=") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// gcReal runs gc without GC_DOLT=skip.
func gcReal(dir string, args ...string) (string, error) {
	cmd := exec.Command(gcBinary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = acceptanceEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// bdReal runs bd without GC_DOLT=skip.
func bdReal(dir string, args ...string) (string, error) {
	cmd := exec.Command(bdBinary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = acceptanceEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// waitForBeadClose polls bd show until the bead is closed or timeout.
func waitForBeadClose(t *testing.T, dir, beadID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastStatus, out string
	var err error
	for time.Now().Before(deadline) {
		out, err = bdReal(dir, "show", beadID)
		if err != nil {
			t.Logf("bd show error (retrying): %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		lower := strings.ToLower(out)
		if strings.Contains(lower, "closed") {
			t.Logf("bead %s closed", beadID)
			return
		}
		status := parseStatus(lower)
		if status != lastStatus {
			t.Logf("bead %s: %s", beadID, status)
			lastStatus = status
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("bead %s did not close within %s:\n%s", beadID, timeout, out)
}

// listUserFiles returns non-infrastructure file names in a directory.
func listUserFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var files []string
	for _, e := range entries {
		switch e.Name() {
		case ".beads", ".gitignore", ".git", ".gc", ".runtime":
			continue
		}
		files = append(files, e.Name())
	}
	return files
}

// extractSlingBeadID extracts the bead ID from gc sling output.
func extractSlingBeadID(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Slung") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "Slung" && i+1 < len(fields) {
					return fields[i+1]
				}
			}
		}
		if strings.Contains(line, "Created") {
			fields := strings.Fields(line)
			for i, f := range fields {
				if f == "Created" && i+1 < len(fields) {
					return strings.TrimRight(fields[i+1], " —")
				}
			}
		}
	}
	t.Fatalf("could not extract bead ID from sling output:\n%s", output)
	return ""
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
