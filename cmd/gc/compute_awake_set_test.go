package main

import (
	"testing"
	"time"
)

var now = time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)

func assertAwake(t *testing.T, result map[string]AwakeDecision, sessionName string) {
	t.Helper()
	d, ok := result[sessionName]
	if !ok {
		t.Errorf("session %q not in result", sessionName)
		return
	}
	if !d.ShouldWake {
		t.Errorf("session %q should be awake but isn't (reason: %s)", sessionName, d.Reason)
	}
}

func assertAsleep(t *testing.T, result map[string]AwakeDecision, sessionName string) {
	t.Helper()
	d, ok := result[sessionName]
	if !ok {
		return // not in result = not awake = correct
	}
	if d.ShouldWake {
		t.Errorf("session %q should be asleep but is awake (reason: %s)", sessionName, d.Reason)
	}
}

func assertReason(t *testing.T, result map[string]AwakeDecision, sessionName, wantReason string) {
	t.Helper()
	d, ok := result[sessionName]
	if !ok {
		t.Errorf("session %q not in result", sessionName)
		return
	}
	if d.Reason != wantReason {
		t.Errorf("session %q reason = %q, want %q", sessionName, d.Reason, wantReason)
	}
}

// ---------------------------------------------------------------------------
// Named session (always)
// ---------------------------------------------------------------------------

func TestNamedAlways_AsleepWakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep", NamedIdentity: "deacon"}},
		Now:           now,
	})
	assertAwake(t, result, "deacon")
}

func TestNamedAlways_ActiveStaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:          []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions:   []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads:    []AwakeSessionBead{{ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "active", NamedIdentity: "deacon"}},
		RunningSessions: map[string]bool{"deacon": true},
		Now:             now,
	})
	assertAwake(t, result, "deacon")
}

func TestNamedAlways_NoBead(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads:  []AwakeSessionBead{},
		Now:           now,
	})
	if len(result) != 0 {
		t.Errorf("expected empty result (no beads), got %d", len(result))
	}
}

func TestNamedAlways_Quarantined(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep",
			NamedIdentity: "deacon", QuarantinedUntil: now.Add(5 * time.Minute),
		}},
		Now: now,
	})
	assertAsleep(t, result, "deacon")
}

func TestNamedAlways_TemplateRemoved(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{},
		NamedSessions: []AwakeNamedSession{},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep", NamedIdentity: "deacon"}},
		Now:           now,
	})
	assertAsleep(t, result, "deacon")
}

// ---------------------------------------------------------------------------
// Named session (on_demand)
// ---------------------------------------------------------------------------

func TestNamedOnDemand_NoWork(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		Now:           now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_AssigneeMatches(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:     []AwakeWorkBead{{ID: "hw-1", Assignee: "hello-world/refinery", Status: "open"}},
		Now:           now,
	})
	assertAwake(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_WorkDone_Drains(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:          []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions:   []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:    []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "active", NamedIdentity: "hello-world/refinery"}},
		RunningSessions: map[string]bool{"hello-world--refinery": true},
		Now:             now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_Attached_StaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions:    []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:     []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "active", NamedIdentity: "hello-world/refinery"}},
		RunningSessions:  map[string]bool{"hello-world--refinery": true},
		AttachedSessions: map[string]bool{"hello-world--refinery": true},
		Now:              now,
	})
	assertAwake(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_ScaleCheckIrrelevant(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions:    []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:     []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		ScaleCheckCounts: map[string]int{"hello-world/refinery": 1},
		Now:              now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

// ---------------------------------------------------------------------------
// Agent template (scaled)
// ---------------------------------------------------------------------------

func TestScaled_NoDemand_NoBeads(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 0},
		Now:              now,
	})
	if len(result) != 0 {
		t.Errorf("expected no decisions, got %d", len(result))
	}
}

func TestScaled_Demand1_NoBeads(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	if len(result) != 0 {
		t.Errorf("expected no decisions (no beads yet), got %d", len(result))
	}
}

func TestScaled_Demand2_OneActive(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-2", SessionName: "polecat-mc-2", Template: "hello-world/polecat", State: "asleep"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 2},
		RunningSessions:  map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
	assertAsleep(t, result, "polecat-mc-2") // asleep ephemerals not reused
}

func TestScaled_Demand1_TwoActive(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-2", SessionName: "polecat-mc-2", Template: "hello-world/polecat", State: "active"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{"polecat-mc-1": true, "polecat-mc-2": true},
		Now:              now,
	})
	awake := 0
	for _, d := range result {
		if d.ShouldWake {
			awake++
		}
	}
	if awake != 1 {
		t.Errorf("expected 1 awake (capped), got %d", awake)
	}
}

func TestScaled_Demand0_OneActive(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 0},
		RunningSessions:  map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-1")
}

func TestScaled_CreatingBead(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestScaled_AsleepEphemeral_NotReused(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-old", SessionName: "polecat-mc-old", Template: "hello-world/polecat", State: "asleep"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-old")
}

func TestScaled_MultipleCapped(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-2", SessionName: "polecat-mc-2", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-3", SessionName: "polecat-mc-3", Template: "hello-world/polecat", State: "active"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 2},
		RunningSessions:  map[string]bool{"polecat-mc-1": true, "polecat-mc-2": true, "polecat-mc-3": true},
		Now:              now,
	})
	awake := 0
	for _, d := range result {
		if d.ShouldWake {
			awake++
		}
	}
	if awake != 2 {
		t.Errorf("expected 2 awake (capped by scaleCheck), got %d", awake)
	}
}

// ---------------------------------------------------------------------------
// Manual session
// ---------------------------------------------------------------------------

func TestManual_ImplicitAgent(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:       []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "creating", ManualSession: true}},
		Now:          now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestManual_ExplicitAgent(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:       []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{{ID: "mc-1", SessionName: "s-mc-1", Template: "hello-world/polecat", State: "creating", ManualSession: true}},
		Now:          now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestManual_NoDemand_StaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads:     []AwakeSessionBead{{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active", ManualSession: true}},
		ScaleCheckCounts: map[string]int{"gascity/claude": 0},
		RunningSessions:  map[string]bool{"s-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestManual_Closed(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:       []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "closed", ManualSession: true}},
		Now:          now,
	})
	assertAsleep(t, result, "s-mc-1")
}

func TestManual_PendingInteraction(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:          []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads:    []AwakeSessionBead{{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active", ManualSession: true}},
		RunningSessions: map[string]bool{"s-mc-1": true},
		PendingSessions: map[string]bool{"s-mc-1": true},
		Now:             now,
	})
	assertAwake(t, result, "s-mc-1")
}

// ---------------------------------------------------------------------------
// Drained beads
// ---------------------------------------------------------------------------

func TestDrained_NotWokenByDemand(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "asleep", Drained: true},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-1")
}

func TestDrained_WokenByAttach(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "asleep", Drained: true},
		},
		AttachedSessions: map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestDrained_WokenByPending(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "asleep", Drained: true},
		},
		PendingSessions: map[string]bool{"polecat-mc-1": true},
		Now:             now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestDrained_ManualNotWoken(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:       []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "asleep", ManualSession: true, Drained: true}},
		Now:          now,
	})
	assertAsleep(t, result, "s-mc-1")
}

// ---------------------------------------------------------------------------
// Hold
// ---------------------------------------------------------------------------

func TestHeld_SuppressesEverything(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "active",
			NamedIdentity: "deacon", HeldUntil: now.Add(10 * time.Minute),
		}},
		RunningSessions:  map[string]bool{"deacon": true},
		AttachedSessions: map[string]bool{"deacon": true},
		Now:              now,
	})
	assertAsleep(t, result, "deacon")
	assertReason(t, result, "deacon", "held")
}

func TestHeld_Expired_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep",
			NamedIdentity: "deacon", HeldUntil: now.Add(-1 * time.Minute),
		}},
		Now: now,
	})
	assertAwake(t, result, "deacon")
}

// ---------------------------------------------------------------------------
// Wait hold + ready wait
// ---------------------------------------------------------------------------

func TestWaitHold_SuppressesAttachAndPending(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "asleep",
			ManualSession: true, WaitHold: true,
		}},
		AttachedSessions: map[string]bool{"s-mc-1": true},
		PendingSessions:  map[string]bool{"s-mc-1": true},
		Now:              now,
	})
	// Manual session is in desired, but wait_hold doesn't suppress desired.
	// It only suppresses attach and pending.
	assertAwake(t, result, "s-mc-1")
	assertReason(t, result, "s-mc-1", "manual") // woke by desired, not attach
}

func TestWaitHold_DesiredStillWakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep",
			NamedIdentity: "deacon", WaitHold: true,
		}},
		Now: now,
	})
	assertAwake(t, result, "deacon")
}

func TestReadyWait_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "asleep",
			WaitHold: true,
		}},
		ReadyWaitSet: map[string]bool{"mc-1": true},
		Now:          now,
	})
	assertAwake(t, result, "s-mc-1")
	assertReason(t, result, "s-mc-1", "wait-ready")
}

func TestReadyWait_NotReady_StaysAsleep(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "asleep",
			WaitHold: true,
		}},
		ReadyWaitSet: map[string]bool{}, // not ready
		Now:          now,
	})
	assertAsleep(t, result, "s-mc-1")
}

// ---------------------------------------------------------------------------
// Dependency only
// ---------------------------------------------------------------------------

func TestDependencyOnly_NotWokenByDemand(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "asleep", DependencyOnly: true},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-1")
}

// ---------------------------------------------------------------------------
// Dependencies
// ---------------------------------------------------------------------------

func TestDependency_DepRunning(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{
			{QualifiedName: "hello-world/witness"},
			{QualifiedName: "hello-world/polecat", DependsOn: []string{"hello-world/witness"}},
		},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-w", SessionName: "hello-world--witness", Template: "hello-world/witness", State: "active"},
			{ID: "mc-p", SessionName: "polecat-mc-p", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{"hello-world--witness": true},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-p")
}

func TestDependency_DepNotRunning(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{
			{QualifiedName: "hello-world/witness"},
			{QualifiedName: "hello-world/polecat", DependsOn: []string{"hello-world/witness"}},
		},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-w", SessionName: "hello-world--witness", Template: "hello-world/witness", State: "asleep"},
			{ID: "mc-p", SessionName: "polecat-mc-p", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-p")
}

// ---------------------------------------------------------------------------
// Idle sleep
// ---------------------------------------------------------------------------

func TestIdleSleep_ManualSession_Sleeps(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active",
				ManualSession: true, IdleSince: now.Add(-20 * time.Minute),
			},
		},
		RunningSessions: map[string]bool{"s-mc-1": true},
		ChatIdleTimeout: 15 * time.Minute,
		Now:             now,
	})
	assertAsleep(t, result, "s-mc-1")
}

func TestIdleSleep_ManualSession_NotLongEnough(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active",
				ManualSession: true, IdleSince: now.Add(-5 * time.Minute),
			},
		},
		RunningSessions: map[string]bool{"s-mc-1": true},
		ChatIdleTimeout: 15 * time.Minute,
		Now:             now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestIdleSleep_ManualSession_Attached_NeverSleeps(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active",
				ManualSession: true, IdleSince: now.Add(-1 * time.Hour),
			},
		},
		RunningSessions:  map[string]bool{"s-mc-1": true},
		AttachedSessions: map[string]bool{"s-mc-1": true},
		ChatIdleTimeout:  15 * time.Minute,
		Now:              now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestIdleSleep_Disabled_NeverSleeps(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active",
				ManualSession: true, IdleSince: now.Add(-24 * time.Hour),
			},
		},
		RunningSessions: map[string]bool{"s-mc-1": true},
		ChatIdleTimeout: 0,
		Now:             now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestIdleSleep_AgentSleepAfterIdle(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat", SleepAfterIdle: 2 * time.Hour}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active",
				IdleSince: now.Add(-3 * time.Hour),
			},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-1")
}

func TestIdleSleep_AgentNotIdleEnough(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat", SleepAfterIdle: 2 * time.Hour}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active",
				IdleSince: now.Add(-30 * time.Minute),
			},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestIdleSleep_OnDemandNamed(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery", SleepAfterIdle: 30 * time.Minute}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads: []AwakeSessionBead{
			{
				ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "active",
				NamedIdentity: "hello-world/refinery", IdleSince: now.Add(-1 * time.Hour),
			},
		},
		WorkBeads:       []AwakeWorkBead{{ID: "hw-1", Assignee: "hello-world/refinery", Status: "open"}},
		RunningSessions: map[string]bool{"hello-world--refinery": true},
		Now:             now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

// ---------------------------------------------------------------------------
// Bug regressions
// ---------------------------------------------------------------------------

func TestRegression_PoolManagedCreatingBead(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestRegression_ManualSessionNotDrained(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active", ManualSession: true},
		},
		ScaleCheckCounts: map[string]int{"gascity/claude": 0},
		RunningSessions:  map[string]bool{"s-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestRegression_OnDemandRefineryAssignee(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:     []AwakeWorkBead{{ID: "hw-1", Assignee: "hello-world/refinery", Status: "open"}},
		Now:           now,
	})
	assertAwake(t, result, "hello-world--refinery")
}
