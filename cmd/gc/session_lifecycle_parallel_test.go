package main

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
)

type gatedStartProvider struct {
	*runtime.Fake
	mu            sync.Mutex
	inFlight      int
	maxInFlight   int
	started       []string
	startSignals  chan string
	releaseByName map[string]chan struct{}
}

func newGatedStartProvider() *gatedStartProvider {
	return &gatedStartProvider{
		Fake:          runtime.NewFake(),
		startSignals:  make(chan string, 32),
		releaseByName: make(map[string]chan struct{}),
	}
}

func (p *gatedStartProvider) Start(ctx context.Context, name string, cfg runtime.Config) error {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	p.started = append(p.started, name)
	ch := p.releaseByName[name]
	if ch == nil {
		ch = make(chan struct{})
		p.releaseByName[name] = ch
	}
	p.mu.Unlock()

	p.startSignals <- name

	select {
	case <-ch:
	case <-ctx.Done():
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
		return ctx.Err()
	}

	err := p.Fake.Start(ctx, name, cfg)
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
	return err
}

func (p *gatedStartProvider) release(name string) {
	p.mu.Lock()
	ch := p.releaseByName[name]
	p.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (p *gatedStartProvider) waitForStarts(t *testing.T, n int) []string {
	t.Helper()
	var names []string
	timeout := time.After(3 * time.Second)
	for len(names) < n {
		select {
		case name := <-p.startSignals:
			names = append(names, name)
		case <-timeout:
			t.Fatalf("timed out waiting for %d starts, got %v", n, names)
		}
	}
	return names
}

func (p *gatedStartProvider) ensureNoFurtherStart(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case name := <-p.startSignals:
		t.Fatalf("unexpected extra start signal: %s", name)
	case <-time.After(wait):
	}
}

type gatedStopProvider struct {
	*runtime.Fake
	mu            sync.Mutex
	inFlight      int
	maxInFlight   int
	stopSignals   chan string
	interrupts    chan string
	releaseByName map[string]chan struct{}
	releaseInt    map[string]chan struct{}
}

func newGatedStopProvider() *gatedStopProvider {
	return &gatedStopProvider{
		Fake:          runtime.NewFake(),
		stopSignals:   make(chan string, 32),
		interrupts:    make(chan string, 32),
		releaseByName: make(map[string]chan struct{}),
		releaseInt:    make(map[string]chan struct{}),
	}
}

func (p *gatedStopProvider) Stop(name string) error {
	p.mu.Lock()
	p.inFlight++
	if p.inFlight > p.maxInFlight {
		p.maxInFlight = p.inFlight
	}
	ch := p.releaseByName[name]
	if ch == nil {
		ch = make(chan struct{})
		p.releaseByName[name] = ch
	}
	p.mu.Unlock()

	p.stopSignals <- name
	<-ch

	err := p.Fake.Stop(name)
	p.mu.Lock()
	p.inFlight--
	p.mu.Unlock()
	return err
}

func (p *gatedStopProvider) Interrupt(name string) error {
	p.mu.Lock()
	ch := p.releaseInt[name]
	if ch == nil {
		ch = make(chan struct{})
		p.releaseInt[name] = ch
	}
	p.mu.Unlock()

	p.interrupts <- name
	<-ch
	return p.Fake.Interrupt(name)
}

func (p *gatedStopProvider) release(name string) {
	p.mu.Lock()
	ch := p.releaseByName[name]
	p.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (p *gatedStopProvider) releaseInterrupt(name string) {
	p.mu.Lock()
	ch := p.releaseInt[name]
	p.mu.Unlock()
	if ch != nil {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func (p *gatedStopProvider) waitForStops(t *testing.T, n int) []string {
	t.Helper()
	var names []string
	timeout := time.After(3 * time.Second)
	for len(names) < n {
		select {
		case name := <-p.stopSignals:
			names = append(names, name)
		case <-timeout:
			t.Fatalf("timed out waiting for %d stops, got %v", n, names)
		}
	}
	return names
}

func (p *gatedStopProvider) ensureNoFurtherStop(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case name := <-p.stopSignals:
		t.Fatalf("unexpected extra stop signal: %s", name)
	case <-time.After(wait):
	}
}

func (p *gatedStopProvider) waitForInterrupts(t *testing.T, n int) []string {
	t.Helper()
	var names []string
	timeout := time.After(3 * time.Second)
	for len(names) < n {
		select {
		case name := <-p.interrupts:
			names = append(names, name)
		case <-timeout:
			t.Fatalf("timed out waiting for %d interrupts, got %v", n, names)
		}
	}
	return names
}

func (p *gatedStopProvider) ensureNoFurtherInterrupt(t *testing.T, wait time.Duration) {
	t.Helper()
	select {
	case name := <-p.interrupts:
		t.Fatalf("unexpected extra interrupt signal: %s", name)
	case <-time.After(wait):
	}
}

type interruptExitProvider struct {
	*runtime.Fake
}

func (p *interruptExitProvider) Interrupt(name string) error {
	if err := p.Fake.Interrupt(name); err != nil {
		return err
	}
	return p.Fake.Stop(name)
}

type dropDependencyAfterNStartsProvider struct {
	*runtime.Fake
	mu        sync.Mutex
	starts    int
	dropAfter int
	depName   string
}

func (p *dropDependencyAfterNStartsProvider) Start(ctx context.Context, name string, cfg runtime.Config) error {
	if err := p.Fake.Start(ctx, name, cfg); err != nil {
		return err
	}
	p.mu.Lock()
	p.starts++
	shouldDrop := p.starts == p.dropAfter
	p.mu.Unlock()
	if shouldDrop {
		_ = p.Fake.Stop(p.depName)
	}
	return nil
}

func containsAll(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]int)
	for _, name := range got {
		seen[name]++
	}
	for _, name := range want {
		if seen[name] == 0 {
			return false
		}
		seen[name]--
	}
	return true
}

func TestReconcileSessionBeads_StartsIndependentWaveInParallelBeforeDependentWave(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", DependsOn: []string{"db", "cache"}},
			{Name: "db"},
			{Name: "cache"},
		},
	}
	store := beads.NewMemStore()
	sp := newGatedStartProvider()
	rec := events.Discard
	clk := &clock.Fake{Time: time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)}
	desired := map[string]TemplateParams{
		"db":     {Command: "db", SessionName: "db", TemplateName: "db"},
		"cache":  {Command: "cache", SessionName: "cache", TemplateName: "cache"},
		"worker": {Command: "worker", SessionName: "worker", TemplateName: "worker"},
	}
	db := makeBead("db-id", map[string]string{
		"session_name": "db", "template": "db", "generation": "1", "instance_token": "tok-db", "state": "asleep",
	})
	cache := makeBead("cache-id", map[string]string{
		"session_name": "cache", "template": "cache", "generation": "1", "instance_token": "tok-cache", "state": "asleep",
	})
	worker := makeBead("worker-id", map[string]string{
		"session_name": "worker", "template": "worker", "generation": "1", "instance_token": "tok-worker", "state": "asleep",
	})
	for _, bead := range []beads.Bead{db, cache, worker} {
		if _, err := store.Create(beads.Bead{
			ID:       bead.ID,
			Title:    bead.Metadata["session_name"],
			Type:     sessionBeadType,
			Labels:   []string{sessionBeadLabel},
			Metadata: bead.Metadata,
		}); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := loadSessionBeads(store)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- reconcileSessionBeads(
			context.Background(), sessions, desired, configuredSessionNames(cfg, "", store),
			cfg, sp, store, nil, nil, nil, newDrainTracker(), map[string]int{}, "",
			nil, clk, rec, 5*time.Second, 0, ioDiscard{}, ioDiscard{},
		)
	}()

	firstWave := sp.waitForStarts(t, 2)
	if !containsAll(firstWave, "db", "cache") {
		t.Fatalf("first wave = %v, want db+cache", firstWave)
	}
	sp.ensureNoFurtherStart(t, 150*time.Millisecond)
	sp.release("db")
	sp.release("cache")

	secondWave := sp.waitForStarts(t, 1)
	if !containsAll(secondWave, "worker") {
		t.Fatalf("second wave = %v, want worker", secondWave)
	}
	sp.release("worker")

	select {
	case woken := <-done:
		if woken != 3 {
			t.Fatalf("woken = %d, want 3", woken)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reconcile did not finish")
	}

	if sp.maxInFlight != 2 {
		t.Fatalf("max in-flight starts = %d, want 2", sp.maxInFlight)
	}
}

func TestReconcileSessionBeads_FailedDependencyBlocksDependentButNotSibling(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{
		Agents: []config.Agent{
			{Name: "worker", DependsOn: []string{"db"}},
			{Name: "db"},
			{Name: "cache"},
		},
	}
	env.addDesired("worker", "worker", false)
	env.addDesired("db", "db", false)
	env.addDesired("cache", "cache", false)
	env.sp.StartErrors["db"] = context.DeadlineExceeded

	woken := env.reconcile([]beads.Bead{
		env.createSessionBead("worker", "worker"),
		env.createSessionBead("db", "db"),
		env.createSessionBead("cache", "cache"),
	})

	if woken != 1 {
		t.Fatalf("woken = %d, want 1", woken)
	}
	if env.sp.IsRunning("worker") {
		t.Fatal("worker should not be running when db failed to start")
	}
	if !env.sp.IsRunning("cache") {
		t.Fatal("cache should still start despite db failure")
	}
}

func TestPrepareStartCandidate_UsesLogicalTemplateForTaskWorkDir(t *testing.T) {
	store := beads.NewMemStore()
	session, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel, "agent:frontend/worker-1"},
		Metadata: map[string]string{
			"template":     "worker",
			"session_name": "custom-worker-1",
			"pool_slot":    "1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	task, err := store.Create(beads.Bead{
		Title: "task",
		Metadata: map[string]string{
			"work_dir": workDir,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status := "in_progress"
	assignee := "frontend/worker"
	if err := store.Update(task.ID, beads.UpdateOpts{Status: &status, Assignee: &assignee}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(workDir); err != nil || !info.IsDir() {
		t.Fatalf("temp workDir not available: %v", err)
	}

	prepared, err := prepareStartCandidate(startCandidate{
		session: &session,
		tp: TemplateParams{
			TemplateName: "frontend/worker",
			SessionName:  "custom-worker-1",
		},
		order: 0,
	}, &config.City{
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", Pool: &config.PoolConfig{Min: 1, Max: 2}},
		},
	}, store, &clock.Fake{Time: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("prepareStartCandidate: %v", err)
	}
	if prepared.cfg.WorkDir != workDir {
		t.Fatalf("prepared.cfg.WorkDir = %q, want %q", prepared.cfg.WorkDir, workDir)
	}
}

func TestReconcileSessionBeads_BlockedCandidatesDoNotConsumeWakeBudget(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{
		Agents: []config.Agent{
			{Name: "blocked", DependsOn: []string{"missing-dep"}},
			{Name: "missing-dep"},
			{Name: "ready-1"},
			{Name: "ready-2"},
			{Name: "ready-3"},
			{Name: "ready-4"},
			{Name: "ready-5"},
		},
	}
	for _, name := range []string{"blocked", "ready-1", "ready-2", "ready-3", "ready-4", "ready-5"} {
		env.addDesired(name, name, false)
	}

	woken := env.reconcile([]beads.Bead{
		env.createSessionBead("blocked", "blocked"),
		env.createSessionBead("ready-1", "ready-1"),
		env.createSessionBead("ready-2", "ready-2"),
		env.createSessionBead("ready-3", "ready-3"),
		env.createSessionBead("ready-4", "ready-4"),
		env.createSessionBead("ready-5", "ready-5"),
	})

	if woken != defaultMaxWakesPerTick {
		t.Fatalf("woken = %d, want %d", woken, defaultMaxWakesPerTick)
	}
	if env.sp.IsRunning("blocked") {
		t.Fatal("blocked session should not have started")
	}
	for _, name := range []string{"ready-1", "ready-2", "ready-3", "ready-4", "ready-5"} {
		if !env.sp.IsRunning(name) {
			t.Fatalf("%s should have started despite blocked candidate ahead of it", name)
		}
	}
}

func TestExecutePlannedStarts_RevalidatesDependenciesBetweenWaveBatches(t *testing.T) {
	sp := &dropDependencyAfterNStartsProvider{
		Fake:      runtime.NewFake(),
		dropAfter: defaultMaxParallelStartsPerWave,
		depName:   "db",
	}
	if err := sp.Start(context.Background(), "db", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "app-1", DependsOn: []string{"db"}},
			{Name: "app-2", DependsOn: []string{"db"}},
			{Name: "app-3", DependsOn: []string{"db"}},
			{Name: "app-4", DependsOn: []string{"db"}},
			{Name: "db"},
		},
	}
	store := beads.NewMemStore()
	clk := &clock.Fake{Time: time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)}
	desired := map[string]TemplateParams{}
	var sessions []beads.Bead
	for _, name := range []string{"app-1", "app-2", "app-3", "app-4"} {
		desired[name] = TemplateParams{Command: name, SessionName: name, TemplateName: name}
		bead := makeBead(name+"-id", map[string]string{
			"session_name":   name,
			"template":       name,
			"generation":     "1",
			"instance_token": "tok-" + name,
			"state":          "asleep",
		})
		created, err := store.Create(beads.Bead{
			ID:       bead.ID,
			Title:    name,
			Type:     sessionBeadType,
			Labels:   []string{sessionBeadLabel},
			Metadata: bead.Metadata,
		})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, created)
	}

	woken := reconcileSessionBeads(
		context.Background(), sessions, desired, configuredSessionNames(cfg, "", store),
		cfg, sp, store, nil, nil, nil, newDrainTracker(), map[string]int{}, "",
		nil, clk, events.Discard, 5*time.Second, 0, ioDiscard{}, ioDiscard{},
	)

	if woken != defaultMaxParallelStartsPerWave {
		t.Fatalf("woken = %d, want %d", woken, defaultMaxParallelStartsPerWave)
	}
	for _, name := range []string{"app-1", "app-2", "app-3"} {
		if !sp.IsRunning(name) {
			t.Fatalf("%s should have started before dependency loss", name)
		}
	}
	if sp.IsRunning("app-4") {
		t.Fatal("app-4 should be blocked after db dies between wave batches")
	}
}

func TestStopSessionsBounded_StopsDependentsBeforeDependencies(t *testing.T) {
	sp := newGatedStopProvider()
	for _, name := range []string{"db", "api", "worker", "audit"} {
		if err := sp.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", DependsOn: []string{"api"}},
			{Name: "audit", DependsOn: []string{"db"}},
			{Name: "api", DependsOn: []string{"db"}},
			{Name: "db"},
		},
	}
	rec := events.NewFake()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- stopSessionsBounded([]string{"db", "api", "worker", "audit"}, cfg, nil, sp, rec, "gc", &stdout, &stderr)
	}()

	firstWave := sp.waitForStops(t, 1)
	if !containsAll(firstWave, "worker") {
		t.Fatalf("first stop wave = %v, want worker", firstWave)
	}
	sp.ensureNoFurtherStop(t, 150*time.Millisecond)
	sp.release("worker")

	secondWave := sp.waitForStops(t, 2)
	if !containsAll(secondWave, "api", "audit") {
		t.Fatalf("second stop wave = %v, want api+audit", secondWave)
	}
	sp.release("api")
	sp.release("audit")

	thirdWave := sp.waitForStops(t, 1)
	if !containsAll(thirdWave, "db") {
		t.Fatalf("third stop wave = %v, want db", thirdWave)
	}
	sp.release("db")

	select {
	case stopped := <-done:
		if stopped != 4 {
			t.Fatalf("stopped = %d, want 4", stopped)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stopSessionsBounded did not finish")
	}
}

func TestStopSessionsBounded_UsesSessionBeadTemplateForCustomSessionNames(t *testing.T) {
	sp := newGatedStopProvider()
	store := beads.NewMemStore()
	cfg := &config.City{
		Workspace: config.Workspace{SessionTemplate: "{{.City}}-{{.Agent}}"},
		Agents: []config.Agent{
			{Name: "worker", DependsOn: []string{"db"}},
			{Name: "db"},
		},
	}
	for _, bead := range []beads.Bead{
		{
			Title:  "db",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel},
			Metadata: map[string]string{
				"template":     "db",
				"session_name": "custom-db",
			},
		},
		{
			Title:  "worker",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel},
			Metadata: map[string]string{
				"template":     "worker",
				"session_name": "custom-worker",
			},
		},
	} {
		if _, err := store.Create(bead); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"custom-db", "custom-worker"} {
		if err := sp.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatal(err)
		}
	}
	rec := events.NewFake()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- stopSessionsBounded([]string{"custom-db", "custom-worker"}, cfg, store, sp, rec, "gc", &stdout, &stderr)
	}()

	firstWave := sp.waitForStops(t, 1)
	if !containsAll(firstWave, "custom-worker") {
		t.Fatalf("first stop wave = %v, want custom-worker", firstWave)
	}
	sp.ensureNoFurtherStop(t, 150*time.Millisecond)
	sp.release("custom-worker")

	secondWave := sp.waitForStops(t, 1)
	if !containsAll(secondWave, "custom-db") {
		t.Fatalf("second stop wave = %v, want custom-db", secondWave)
	}
	sp.release("custom-db")

	select {
	case stopped := <-done:
		if stopped != 2 {
			t.Fatalf("stopped = %d, want 2", stopped)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stopSessionsBounded did not finish")
	}
}

func TestStopSessionsBounded_UsesLegacyAgentLabelTemplateForOrdering(t *testing.T) {
	sp := newGatedStopProvider()
	store := beads.NewMemStore()
	cfg := &config.City{
		Workspace: config.Workspace{SessionTemplate: "{{.Agent}}"},
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", DependsOn: []string{"frontend/db"}, Pool: &config.PoolConfig{Min: 1, Max: 2}},
			{Name: "db", Dir: "frontend"},
		},
	}
	for _, bead := range []beads.Bead{
		{
			Title:  "db",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel, "agent:frontend/db"},
			Metadata: map[string]string{
				"template":     "frontend/db",
				"session_name": "custom-db",
			},
		},
		{
			Title:  "worker",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel, "agent:frontend/worker-1"},
			Metadata: map[string]string{
				"template":     "worker",
				"session_name": "custom-worker-1",
				"pool_slot":    "1",
			},
		},
	} {
		if _, err := store.Create(bead); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"custom-db", "custom-worker-1"} {
		if err := sp.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatal(err)
		}
	}
	rec := events.NewFake()
	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- stopSessionsBounded([]string{"custom-db", "custom-worker-1"}, cfg, store, sp, rec, "gc", &stdout, &stderr)
	}()

	firstWave := sp.waitForStops(t, 1)
	if !containsAll(firstWave, "custom-worker-1") {
		t.Fatalf("first stop wave = %v, want custom-worker-1", firstWave)
	}
	sp.ensureNoFurtherStop(t, 150*time.Millisecond)
	sp.release("custom-worker-1")

	secondWave := sp.waitForStops(t, 1)
	if !containsAll(secondWave, "custom-db") {
		t.Fatalf("second stop wave = %v, want custom-db", secondWave)
	}
	sp.release("custom-db")

	select {
	case stopped := <-done:
		if stopped != 2 {
			t.Fatalf("stopped = %d, want 2", stopped)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stopSessionsBounded did not finish")
	}
}

func TestInterruptSessionsBounded_BoundsParallelBroadcast(t *testing.T) {
	sp := newGatedStopProvider()
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", DependsOn: []string{"api"}},
			{Name: "audit", DependsOn: []string{"db"}},
			{Name: "api", DependsOn: []string{"db"}},
			{Name: "db"},
		},
	}
	done := make(chan int, 1)
	go func() {
		done <- interruptSessionsBounded([]string{"db", "api", "worker", "audit"}, cfg, nil, sp, ioDiscard{})
	}()

	firstBatch := sp.waitForInterrupts(t, 3)
	if !containsAll(firstBatch, "db", "api", "worker") {
		t.Fatalf("first interrupt batch = %v, want db+api+worker", firstBatch)
	}
	sp.ensureNoFurtherInterrupt(t, 150*time.Millisecond)
	sp.releaseInterrupt("db")
	sp.releaseInterrupt("api")
	sp.releaseInterrupt("worker")

	secondBatch := sp.waitForInterrupts(t, 1)
	if !containsAll(secondBatch, "audit") {
		t.Fatalf("second interrupt batch = %v, want audit", secondBatch)
	}
	sp.releaseInterrupt("audit")

	select {
	case interrupted := <-done:
		if interrupted != 4 {
			t.Fatalf("interrupted = %d, want 4", interrupted)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interruptSessionsBounded did not finish")
	}
}

func TestGracefulStopAll_UsesLogicalSubjectForGracefulExit(t *testing.T) {
	sp := &interruptExitProvider{Fake: runtime.NewFake()}
	store := beads.NewMemStore()
	if _, err := store.Create(beads.Bead{
		Title:  "frontend/worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"template":     "frontend/worker",
			"agent_name":   "frontend/worker",
			"session_name": "custom-worker",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Start(context.Background(), "custom-worker", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	rec := events.NewFake()
	cfg := &config.City{Agents: []config.Agent{{Name: "worker", Dir: "frontend"}}}
	var stdout, stderr bytes.Buffer

	gracefulStopAll([]string{"custom-worker"}, sp, 50*time.Millisecond, rec, cfg, store, &stdout, &stderr)

	if len(rec.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(rec.Events))
	}
	if rec.Events[0].Subject != "frontend/worker" {
		t.Fatalf("event subject = %q, want %q", rec.Events[0].Subject, "frontend/worker")
	}
}

func TestGracefulStopAll_ReconstructsPoolSubjectFromLegacyBead(t *testing.T) {
	sp := &interruptExitProvider{Fake: runtime.NewFake()}
	store := beads.NewMemStore()
	if _, err := store.Create(beads.Bead{
		Title:  "frontend/worker-2",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"template":     "frontend/worker",
			"pool_slot":    "2",
			"session_name": "custom-worker-2",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Start(context.Background(), "custom-worker-2", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	rec := events.NewFake()
	cfg := &config.City{Agents: []config.Agent{{Name: "worker", Dir: "frontend", Pool: &config.PoolConfig{Min: 1, Max: 3}}}}
	var stdout, stderr bytes.Buffer

	gracefulStopAll([]string{"custom-worker-2"}, sp, 50*time.Millisecond, rec, cfg, store, &stdout, &stderr)

	if len(rec.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(rec.Events))
	}
	if rec.Events[0].Subject != "frontend/worker-2" {
		t.Fatalf("event subject = %q, want %q", rec.Events[0].Subject, "frontend/worker-2")
	}
}

func TestGracefulStopAll_UsesLegacyAgentLabelForPoolSubject(t *testing.T) {
	sp := &interruptExitProvider{Fake: runtime.NewFake()}
	store := beads.NewMemStore()
	if _, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel, "agent:frontend/worker-4"},
		Metadata: map[string]string{
			"template":     "worker",
			"pool_slot":    "4",
			"session_name": "custom-worker-4",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Start(context.Background(), "custom-worker-4", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	rec := events.NewFake()
	cfg := &config.City{Agents: []config.Agent{{Name: "worker", Dir: "frontend", Pool: &config.PoolConfig{Min: 1, Max: 5}}}}
	var stdout, stderr bytes.Buffer

	gracefulStopAll([]string{"custom-worker-4"}, sp, 50*time.Millisecond, rec, cfg, store, &stdout, &stderr)

	if len(rec.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(rec.Events))
	}
	if rec.Events[0].Subject != "frontend/worker-4" {
		t.Fatalf("event subject = %q, want %q", rec.Events[0].Subject, "frontend/worker-4")
	}
}

func TestStopWaveOrder_HandlesUnknownTemplateWithoutSerialFallback(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", DependsOn: []string{"db"}},
			{Name: "db"},
		},
	}
	targets := []stopTarget{
		{name: "removed-worker", template: "removed-worker", order: 0},
		{name: "worker", template: "worker", order: 1},
		{name: "db", template: "db", order: 2},
	}

	waves, ok := stopWaveOrder(targets, cfg)
	if !ok {
		t.Fatal("unexpected serial fallback for unknown template")
	}
	if waves[0] != 1 || waves[1] != 0 || waves[2] != 1 {
		t.Fatalf("waves = %#v, want worker in wave 0 and unknown+db in wave 1", waves)
	}
}

func TestStopWaveOrder_PreservesTransitiveSubsetOrdering(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "api", DependsOn: []string{"cache"}},
			{Name: "cache", DependsOn: []string{"db"}},
			{Name: "db"},
		},
	}
	targets := []stopTarget{
		{name: "api", template: "api", order: 0},
		{name: "db", template: "db", order: 1},
	}

	waves, ok := stopWaveOrder(targets, cfg)
	if !ok {
		t.Fatal("unexpected serial fallback for transitive subset")
	}
	if waves[0] != 0 || waves[1] != 1 {
		t.Fatalf("waves = %#v, want api before db via transitive dependency", waves)
	}
}

func TestStopWaveOrder_FallsBackToSerialOnCycle(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "api", DependsOn: []string{"db"}},
			{Name: "db", DependsOn: []string{"api"}},
		},
	}
	targets := []stopTarget{
		{name: "api", template: "api", order: 0},
		{name: "db", template: "db", order: 1},
	}

	waves, ok := stopWaveOrder(targets, cfg)
	if ok {
		t.Fatal("expected serial fallback for cycle")
	}
	if waves[0] != 0 || waves[1] != 1 {
		t.Fatalf("waves = %#v, want strict serial fallback", waves)
	}
}

func TestCandidateWaveOrder_FallsBackToSerialOnCycle(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "api", DependsOn: []string{"db"}},
			{Name: "db", DependsOn: []string{"api"}},
		},
	}
	candidates := []startCandidate{
		{
			session: &beads.Bead{Metadata: map[string]string{"session_name": "api", "template": "api"}},
			tp:      TemplateParams{TemplateName: "api"},
			order:   0,
		},
		{
			session: &beads.Bead{Metadata: map[string]string{"session_name": "db", "template": "db"}},
			tp:      TemplateParams{TemplateName: "db"},
			order:   1,
		},
	}

	waves, ok := candidateWaveOrder(candidates, cfg, map[string]TemplateParams{}, runtime.NewFake(), "city", nil)
	if ok {
		t.Fatal("expected serial fallback for cycle")
	}
	if waves[0] != 0 || waves[1] != 1 {
		t.Fatalf("waves = %#v, want strict serial fallback", waves)
	}
}

func TestCandidateWaveOrder_UsesLegacyAgentLabelTemplate(t *testing.T) {
	store := beads.NewMemStore()
	for _, bead := range []beads.Bead{
		{
			Title:  "db",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel, "agent:frontend/db"},
			Metadata: map[string]string{
				"template":     "frontend/db",
				"session_name": "custom-db",
			},
		},
		{
			Title:  "worker",
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel, "agent:frontend/worker-1"},
			Metadata: map[string]string{
				"template":     "worker",
				"session_name": "custom-worker-1",
				"pool_slot":    "1",
			},
		},
	} {
		if _, err := store.Create(bead); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.City{
		Workspace: config.Workspace{SessionTemplate: "{{.Agent}}"},
		Agents: []config.Agent{
			{Name: "worker", Dir: "frontend", DependsOn: []string{"frontend/db"}, Pool: &config.PoolConfig{Min: 1, Max: 2}},
			{Name: "db", Dir: "frontend"},
		},
	}
	candidates := []startCandidate{
		{
			session: &beads.Bead{
				Labels: []string{sessionBeadLabel, "agent:frontend/worker-1"},
				Metadata: map[string]string{
					"template":     "worker",
					"session_name": "custom-worker-1",
					"pool_slot":    "1",
				},
			},
			tp:    TemplateParams{TemplateName: "frontend/worker"},
			order: 0,
		},
		{
			session: &beads.Bead{Metadata: map[string]string{
				"template":     "frontend/db",
				"session_name": "custom-db",
			}},
			tp:    TemplateParams{TemplateName: "frontend/db"},
			order: 1,
		},
	}

	waves, ok := candidateWaveOrder(candidates, cfg, map[string]TemplateParams{}, runtime.NewFake(), "city", store)
	if !ok {
		t.Fatal("unexpected serial fallback")
	}
	if waves[0] != 1 || waves[1] != 0 {
		t.Fatalf("waves = %#v, want legacy worker after db", waves)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
