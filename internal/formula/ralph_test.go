package formula

import "testing"

func TestApplyRalph_Basic(t *testing.T) {
	steps := []*Step{
		{
			ID:          "implement",
			Title:       "Implement widget",
			Description: "Make the code changes.",
			Type:        "task",
			DependsOn:   []string{"design"},
			Needs:       []string{"setup"},
			Labels:      []string{"frontend"},
			Metadata: map[string]string{
				"custom": "value",
			},
			Ralph: &RalphSpec{
				MaxAttempts: 3,
				Check: &RalphCheckSpec{
					Mode:    "exec",
					Path:    ".gascity/checks/widget.sh",
					Timeout: "2m",
				},
			},
		},
	}

	got, err := ApplyRalph(steps)
	if err != nil {
		t.Fatalf("ApplyRalph failed: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}

	logical := got[0]
	run := got[1]
	check := got[2]

	if logical.ID != "implement" {
		t.Fatalf("logical.ID = %q, want implement", logical.ID)
	}
	if run.ID != "implement.run.1" {
		t.Fatalf("run.ID = %q, want implement.run.1", run.ID)
	}
	if check.ID != "implement.check.1" {
		t.Fatalf("check.ID = %q, want implement.check.1", check.ID)
	}

	if logical.Metadata["gc.kind"] != "ralph" {
		t.Errorf("logical gc.kind = %q, want ralph", logical.Metadata["gc.kind"])
	}
	if logical.Metadata["gc.max_attempts"] != "3" {
		t.Errorf("logical gc.max_attempts = %q, want 3", logical.Metadata["gc.max_attempts"])
	}
	if run.Metadata["gc.kind"] != "run" {
		t.Errorf("run gc.kind = %q, want run", run.Metadata["gc.kind"])
	}
	if run.Metadata["gc.step_id"] != "implement" {
		t.Errorf("run gc.step_id = %q, want implement", run.Metadata["gc.step_id"])
	}
	if run.Metadata["gc.attempt"] != "1" {
		t.Errorf("run gc.attempt = %q, want 1", run.Metadata["gc.attempt"])
	}
	if run.Metadata["custom"] != "value" {
		t.Errorf("run custom metadata = %q, want value", run.Metadata["custom"])
	}
	if check.Metadata["gc.kind"] != "check" {
		t.Errorf("check gc.kind = %q, want check", check.Metadata["gc.kind"])
	}
	if check.Metadata["gc.check_mode"] != "exec" {
		t.Errorf("check gc.check_mode = %q, want exec", check.Metadata["gc.check_mode"])
	}
	if check.Metadata["gc.check_path"] != ".gascity/checks/widget.sh" {
		t.Errorf("check gc.check_path = %q, want .gascity/checks/widget.sh", check.Metadata["gc.check_path"])
	}
	if check.Metadata["gc.check_timeout"] != "2m" {
		t.Errorf("check gc.check_timeout = %q, want 2m", check.Metadata["gc.check_timeout"])
	}

	if len(run.DependsOn) != 1 || run.DependsOn[0] != "design" {
		t.Errorf("run.DependsOn = %v, want [design]", run.DependsOn)
	}
	if len(run.Needs) != 1 || run.Needs[0] != "setup" {
		t.Errorf("run.Needs = %v, want [setup]", run.Needs)
	}

	wantLogicalNeeds := map[string]bool{"setup": true, "implement.check.1": true}
	if len(logical.Needs) != 2 {
		t.Fatalf("logical.Needs = %v, want two entries", logical.Needs)
	}
	for _, need := range logical.Needs {
		if !wantLogicalNeeds[need] {
			t.Errorf("logical.Needs contains unexpected %q", need)
		}
	}
	if len(check.Needs) != 1 || check.Needs[0] != "implement.run.1" {
		t.Errorf("check.Needs = %v, want [implement.run.1]", check.Needs)
	}
}
