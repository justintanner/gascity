package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

func TestSessionWake_ClearsMetadata(t *testing.T) {
	// Test the wake logic directly: clear held_until, quarantined_until,
	// wake_attempts, and sleep_reason via SetMetadataBatch.
	store := beads.NewMemStore()
	b, _ := store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"template":          "worker",
			"held_until":        "9999-12-31T23:59:59Z",
			"quarantined_until": "9999-12-31T23:59:59Z",
			"wake_attempts":     "5",
			"sleep_reason":      "user-hold",
		},
	})

	// Simulate what cmdSessionWake does.
	batch := map[string]string{
		"held_until":        "",
		"quarantined_until": "",
		"wake_attempts":     "0",
	}
	sr := b.Metadata["sleep_reason"]
	if sr == "user-hold" || sr == "quarantine" {
		batch["sleep_reason"] = ""
	}
	if err := store.SetMetadataBatch(b.ID, batch); err != nil {
		t.Fatalf("SetMetadataBatch: %v", err)
	}

	updated, _ := store.Get(b.ID)
	if got := updated.Metadata["held_until"]; got != "" {
		t.Errorf("held_until should be cleared, got %q", got)
	}
	if got := updated.Metadata["quarantined_until"]; got != "" {
		t.Errorf("quarantined_until should be cleared, got %q", got)
	}
	if got := updated.Metadata["wake_attempts"]; got != "0" {
		t.Errorf("wake_attempts should be 0, got %q", got)
	}
	if got := updated.Metadata["sleep_reason"]; got != "" {
		t.Errorf("sleep_reason should be cleared, got %q", got)
	}
}

func TestSessionWake_PreservesNonHoldSleepReason(t *testing.T) {
	store := beads.NewMemStore()
	b, _ := store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"template":     "worker",
			"sleep_reason": "idle",
		},
	})

	// wake should NOT clear sleep_reason when it's "idle" (not hold/quarantine).
	batch := map[string]string{
		"held_until":        "",
		"quarantined_until": "",
		"wake_attempts":     "0",
	}
	sr := b.Metadata["sleep_reason"]
	if sr == "user-hold" || sr == "quarantine" {
		batch["sleep_reason"] = ""
	}
	if err := store.SetMetadataBatch(b.ID, batch); err != nil {
		t.Fatalf("SetMetadataBatch: %v", err)
	}

	updated, _ := store.Get(b.ID)
	if got := updated.Metadata["sleep_reason"]; got != "idle" {
		t.Errorf("sleep_reason should be preserved as 'idle', got %q", got)
	}
}
