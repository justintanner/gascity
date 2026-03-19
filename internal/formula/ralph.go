package formula

import (
	"fmt"
	"strconv"
)

// ApplyRalph expands inline Ralph steps into ordinary graph nodes.
//
// A Ralph step:
//   - keeps its original step ID as the stable logical step
//   - emits a first run attempt:  <step>.run.1
//   - emits a first check attempt: <step>.check.1
//
// The generated graph uses only ordinary blocking deps:
//   - check blocks on run
//   - logical step blocks on check
//
// Downstream steps continue to depend on the original logical step ID.
func ApplyRalph(steps []*Step) ([]*Step, error) {
	result := make([]*Step, 0, len(steps))

	for _, step := range steps {
		if step.Ralph == nil {
			clone := cloneStep(step)
			if len(step.Children) > 0 {
				children, err := ApplyRalph(step.Children)
				if err != nil {
					return nil, err
				}
				clone.Children = children
			}
			result = append(result, clone)
			continue
		}

		expanded, err := expandRalph(step)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}

	return result, nil
}

func expandRalph(step *Step) ([]*Step, error) {
	if step.Ralph == nil {
		return nil, fmt.Errorf("expanding ralph: step %q missing ralph spec", step.ID)
	}
	if step.Ralph.Check == nil {
		return nil, fmt.Errorf("expanding ralph %q: missing check spec", step.ID)
	}

	attempt := 1
	runID := fmt.Sprintf("%s.run.%d", step.ID, attempt)
	checkID := fmt.Sprintf("%s.check.%d", step.ID, attempt)

	logical := cloneStep(step)
	logical.Ralph = nil
	logical.Metadata = withMetadata(logical.Metadata, map[string]string{
		"gc.kind":         "ralph",
		"gc.step_id":      step.ID,
		"gc.max_attempts": strconv.Itoa(step.Ralph.MaxAttempts),
	})
	logical.Needs = appendUniqueCopy(logical.Needs, checkID)
	logical.WaitsFor = ""

	run := cloneStep(step)
	run.ID = runID
	run.Ralph = nil
	run.Metadata = withMetadata(run.Metadata, map[string]string{
		"gc.kind":    "run",
		"gc.step_id": step.ID,
		"gc.attempt": strconv.Itoa(attempt),
	})
	run.SourceLocation = fmt.Sprintf("%s.ralph.run.%d", step.SourceLocation, attempt)

	check := &Step{
		ID:            checkID,
		Title:         fmt.Sprintf("Check %s", step.Title),
		Description:   fmt.Sprintf("Validate %s attempt %d", step.ID, attempt),
		Type:          "task",
		Priority:      step.Priority,
		Labels:        append([]string{}, step.Labels...),
		Needs:         []string{runID},
		Condition:     step.Condition,
		SourceFormula: step.SourceFormula,
		SourceLocation: func() string {
			if step.SourceLocation == "" {
				return fmt.Sprintf("ralph.check.%d", attempt)
			}
			return fmt.Sprintf("%s.ralph.check.%d", step.SourceLocation, attempt)
		}(),
		Metadata: withMetadata(nil, map[string]string{
			"gc.kind":          "check",
			"gc.step_id":       step.ID,
			"gc.attempt":       strconv.Itoa(attempt),
			"gc.check_mode":    step.Ralph.Check.Mode,
			"gc.check_path":    step.Ralph.Check.Path,
			"gc.check_timeout": step.Ralph.Check.Timeout,
			"gc.max_attempts":  strconv.Itoa(step.Ralph.MaxAttempts),
		}),
	}

	return []*Step{logical, run, check}, nil
}

func withMetadata(base map[string]string, extra map[string]string) map[string]string {
	size := len(base) + len(extra)
	if size == 0 {
		return nil
	}
	out := make(map[string]string, size)
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func appendUniqueCopy(slice []string, item string) []string {
	if item == "" {
		if len(slice) == 0 {
			return nil
		}
		out := make([]string, len(slice))
		copy(out, slice)
		return out
	}
	for _, s := range slice {
		if s == item {
			out := make([]string, len(slice))
			copy(out, slice)
			return out
		}
	}
	out := make([]string, 0, len(slice)+1)
	out = append(out, slice...)
	out = append(out, item)
	return out
}
