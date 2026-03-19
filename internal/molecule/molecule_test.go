package molecule

import (
	"context"
	"fmt"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/formula"
)

func TestInstantiateSimple(t *testing.T) {
	store := beads.NewMemStore()
	recipe := &formula.Recipe{
		Name:        "test-formula",
		Description: "A test formula",
		Steps: []formula.RecipeStep{
			{ID: "test-formula", Title: "{{title}}", Type: "epic", IsRoot: true},
			{ID: "test-formula.step-a", Title: "Step A", Type: "task"},
			{ID: "test-formula.step-b", Title: "Step B: {{feature}}", Type: "task"},
		},
		Deps: []formula.RecipeDep{
			{StepID: "test-formula.step-a", DependsOnID: "test-formula", Type: "parent-child"},
			{StepID: "test-formula.step-b", DependsOnID: "test-formula", Type: "parent-child"},
			{StepID: "test-formula.step-b", DependsOnID: "test-formula.step-a", Type: "blocks"},
		},
		Vars: map[string]*formula.VarDef{
			"title":   {Description: "Title"},
			"feature": {Description: "Feature name"},
		},
	}

	result, err := Instantiate(context.Background(), store, recipe, Options{
		Title: "My Feature",
		Vars:  map[string]string{"feature": "auth"},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	if result.Created != 3 {
		t.Errorf("Created = %d, want 3", result.Created)
	}
	if result.RootID == "" {
		t.Fatal("RootID is empty")
	}

	// Verify root bead
	root, err := store.Get(result.RootID)
	if err != nil {
		t.Fatalf("Get root: %v", err)
	}
	if root.Title != "My Feature" {
		t.Errorf("root.Title = %q, want %q", root.Title, "My Feature")
	}
	if root.Type != "molecule" {
		t.Errorf("root.Type = %q, want %q", root.Type, "molecule")
	}
	if root.Ref != "test-formula" {
		t.Errorf("root.Ref = %q, want %q", root.Ref, "test-formula")
	}

	// Verify step-b has variable substitution
	stepBID := result.IDMapping["test-formula.step-b"]
	stepB, err := store.Get(stepBID)
	if err != nil {
		t.Fatalf("Get step-b: %v", err)
	}
	if stepB.Title != "Step B: auth" {
		t.Errorf("step-b.Title = %q, want %q", stepB.Title, "Step B: auth")
	}
	if stepB.ParentID != result.RootID {
		t.Errorf("step-b.ParentID = %q, want %q", stepB.ParentID, result.RootID)
	}
}

func TestInstantiateWithParentID(t *testing.T) {
	store := beads.NewMemStore()

	// Create a parent bead first
	parent, err := store.Create(beads.Bead{Title: "Parent"})
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}

	recipe := &formula.Recipe{
		Name: "child-formula",
		Steps: []formula.RecipeStep{
			{ID: "child-formula", Title: "Child", Type: "epic", IsRoot: true},
		},
	}

	result, err := Instantiate(context.Background(), store, recipe, Options{
		ParentID: parent.ID,
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	root, _ := store.Get(result.RootID)
	if root.ParentID != parent.ID {
		t.Errorf("root.ParentID = %q, want %q", root.ParentID, parent.ID)
	}
}

func TestInstantiateWithIdempotencyKey(t *testing.T) {
	store := beads.NewMemStore()
	recipe := &formula.Recipe{
		Name: "idem-formula",
		Steps: []formula.RecipeStep{
			{ID: "idem-formula", Title: "Root", Type: "epic", IsRoot: true},
		},
	}

	result, err := Instantiate(context.Background(), store, recipe, Options{
		IdempotencyKey: "converge:abc:iter:1",
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	root, _ := store.Get(result.RootID)
	if root.Metadata["idempotency_key"] != "converge:abc:iter:1" {
		t.Errorf("idempotency_key = %q, want %q", root.Metadata["idempotency_key"], "converge:abc:iter:1")
	}
}

func TestInstantiateRootOnly(t *testing.T) {
	store := beads.NewMemStore()
	recipe := &formula.Recipe{
		Name:     "patrol",
		RootOnly: true,
		Steps: []formula.RecipeStep{
			{ID: "patrol", Title: "Patrol", Type: "epic", IsRoot: true},
			{ID: "patrol.scan", Title: "Scan", Type: "task"},
		},
		Deps: []formula.RecipeDep{
			{StepID: "patrol.scan", DependsOnID: "patrol", Type: "parent-child"},
		},
	}

	result, err := Instantiate(context.Background(), store, recipe, Options{})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	if result.Created != 1 {
		t.Errorf("Created = %d, want 1 (root only)", result.Created)
	}

	all, _ := store.List()
	if len(all) != 1 {
		t.Errorf("store has %d beads, want 1", len(all))
	}
}

func TestInstantiateVarDefaults(t *testing.T) {
	store := beads.NewMemStore()
	defaultVal := "default-branch"
	recipe := &formula.Recipe{
		Name: "var-test",
		Steps: []formula.RecipeStep{
			{ID: "var-test", Title: "{{title}}", Type: "epic", IsRoot: true},
			{ID: "var-test.step", Title: "Branch: {{branch}}", Type: "task"},
		},
		Deps: []formula.RecipeDep{
			{StepID: "var-test.step", DependsOnID: "var-test", Type: "parent-child"},
		},
		Vars: map[string]*formula.VarDef{
			"title":  {Description: "Title"},
			"branch": {Description: "Branch", Default: &defaultVal},
		},
	}

	// Don't provide "branch" — should use default
	result, err := Instantiate(context.Background(), store, recipe, Options{
		Vars: map[string]string{"title": "My Thing"},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}

	stepID := result.IDMapping["var-test.step"]
	step, _ := store.Get(stepID)
	if step.Title != "Branch: default-branch" {
		t.Errorf("step.Title = %q, want %q", step.Title, "Branch: default-branch")
	}
}

func TestInstantiateNilRecipe(t *testing.T) {
	store := beads.NewMemStore()
	_, err := Instantiate(context.Background(), store, nil, Options{})
	if err == nil {
		t.Fatal("expected error for nil recipe")
	}
}

func TestInstantiateEmptyRecipe(t *testing.T) {
	store := beads.NewMemStore()
	_, err := Instantiate(context.Background(), store, &formula.Recipe{Name: "empty"}, Options{})
	if err == nil {
		t.Fatal("expected error for empty recipe")
	}
}

// errStore fails on the Nth Create call.
type errStore struct {
	beads.Store
	failOnCreate int
	createCount  int
}

func (e *errStore) Create(b beads.Bead) (beads.Bead, error) {
	e.createCount++
	if e.createCount == e.failOnCreate {
		return beads.Bead{}, fmt.Errorf("injected create failure")
	}
	return e.Store.Create(b)
}

func TestInstantiateCreateFailure(t *testing.T) {
	base := beads.NewMemStore()
	store := &errStore{Store: base, failOnCreate: 2} // fail on second create (first step)

	recipe := &formula.Recipe{
		Name: "fail-test",
		Steps: []formula.RecipeStep{
			{ID: "fail-test", Title: "Root", Type: "epic", IsRoot: true},
			{ID: "fail-test.step", Title: "Step", Type: "task"},
		},
		Deps: []formula.RecipeDep{
			{StepID: "fail-test.step", DependsOnID: "fail-test", Type: "parent-child"},
		},
	}

	_, err := Instantiate(context.Background(), store, recipe, Options{})
	if err == nil {
		t.Fatal("expected error on create failure")
	}

	// Root bead should exist but be marked as failed
	all, _ := base.List()
	if len(all) != 1 {
		t.Fatalf("expected 1 bead (root), got %d", len(all))
	}
	root, _ := base.Get(all[0].ID)
	if root.Metadata["molecule_failed"] != "true" {
		t.Error("root bead not marked as molecule_failed")
	}
}

// errDepStore fails on DepAdd.
type errDepStore struct {
	beads.Store
}

func (e *errDepStore) DepAdd(_, _, _ string) error {
	return fmt.Errorf("injected dep failure")
}

func TestInstantiateDepFailure(t *testing.T) {
	base := beads.NewMemStore()
	store := &errDepStore{Store: base}

	recipe := &formula.Recipe{
		Name: "dep-fail",
		Steps: []formula.RecipeStep{
			{ID: "dep-fail", Title: "Root", Type: "epic", IsRoot: true},
			{ID: "dep-fail.a", Title: "A", Type: "task"},
			{ID: "dep-fail.b", Title: "B", Type: "task"},
		},
		Deps: []formula.RecipeDep{
			{StepID: "dep-fail.a", DependsOnID: "dep-fail", Type: "parent-child"},
			{StepID: "dep-fail.b", DependsOnID: "dep-fail", Type: "parent-child"},
			{StepID: "dep-fail.b", DependsOnID: "dep-fail.a", Type: "blocks"},
		},
	}

	_, err := Instantiate(context.Background(), store, recipe, Options{})
	if err == nil {
		t.Fatal("expected error on dep failure")
	}

	// All beads should be marked as failed
	all, _ := base.List()
	for _, b := range all {
		full, _ := base.Get(b.ID)
		if full.Metadata["molecule_failed"] != "true" {
			t.Errorf("bead %s not marked as molecule_failed", b.ID)
		}
	}
}

func TestCookOnRequiresParentID(t *testing.T) {
	store := beads.NewMemStore()
	_, err := CookOn(context.Background(), store, "x", nil, Options{})
	if err == nil {
		t.Fatal("expected error when ParentID is empty")
	}
}
