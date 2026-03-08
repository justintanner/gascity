package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

func TestResolveSessionID_BeadID(t *testing.T) {
	store := beads.NewMemStore()
	id, err := resolveSessionID(store, "gc-42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "gc-42" {
		t.Errorf("got %q, want gc-42", id)
	}
}

func TestResolveSessionID_TemplateName(t *testing.T) {
	store := beads.NewMemStore()
	b, _ := store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession, "template:overseer"},
		Metadata: map[string]string{
			"template": "overseer",
		},
	})

	id, err := resolveSessionID(store, "overseer")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != b.ID {
		t.Errorf("got %q, want %q", id, b.ID)
	}
}

func TestResolveSessionID_QualifiedName(t *testing.T) {
	store := beads.NewMemStore()
	b, _ := store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession, "template:myrig/worker"},
		Metadata: map[string]string{
			"template": "myrig/worker",
		},
	})

	// Resolve by bare name.
	id, err := resolveSessionID(store, "worker")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != b.ID {
		t.Errorf("got %q, want %q", id, b.ID)
	}

	// Resolve by qualified name.
	id, err = resolveSessionID(store, "myrig/worker")
	if err != nil {
		t.Fatalf("unexpected error for qualified: %v", err)
	}
	if id != b.ID {
		t.Errorf("qualified: got %q, want %q", id, b.ID)
	}
}

func TestResolveSessionID_Ambiguous(t *testing.T) {
	store := beads.NewMemStore()
	_, _ = store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"template": "worker",
		},
	})
	_, _ = store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"template": "worker",
		},
	})

	_, err := resolveSessionID(store, "worker")
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention ambiguous, got: %v", err)
	}
}

func TestResolveSessionID_NotFound(t *testing.T) {
	store := beads.NewMemStore()
	_, err := resolveSessionID(store, "nonexistent")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !contains(err.Error(), "no session found") {
		t.Errorf("error should mention not found, got: %v", err)
	}
}

func TestResolveSessionID_SkipsClosedBeads(t *testing.T) {
	store := beads.NewMemStore()
	b, _ := store.Create(beads.Bead{
		Type:   session.BeadType,
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"template": "worker",
		},
	})
	_ = store.Close(b.ID)

	_, err := resolveSessionID(store, "worker")
	if err == nil {
		t.Fatal("expected not found for closed session")
	}
}

func TestLooksLikeBeadID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"gc-42", true},
		{"gc-1", true},
		{"gc-abc", true},
		{"overseer", false},
		{"myrig/worker", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := looksLikeBeadID(tt.input); got != tt.want {
			t.Errorf("looksLikeBeadID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && containsHelper(s, sub)
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
