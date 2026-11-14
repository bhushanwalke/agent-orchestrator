package plan_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// flowFixture runs the real scheduler (the service is its own scheduler).
func flowFixture(t *testing.T, config domain.ProjectConfig) fixture {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", DisplayName: "Mercury", Config: config,
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{sessions: map[domain.SessionID]domain.Session{}}
	n := 0
	svc := plan.New(plan.Deps{
		Store: st, Sessions: sessions, Projects: st, KickoffRetryInterval: -1,
		Now:   func() time.Time { return time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC) },
		NewID: func(prefix string) string { n++; return fmt.Sprintf("%s%d", prefix, n) },
	})
	return fixture{svc: svc, store: st, sessions: sessions}
}

type msSpec struct {
	key    string
	deps   []string
	sparse []string
}

// activePlan creates a plan with the milestones and activates it, which runs
// the first scheduler tick.
func activePlan(t *testing.T, f fixture, maxParallel int, specs ...msSpec) domain.PlanID {
	t.Helper()
	ctx := context.Background()
	p, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "Todo", Brief: "Build it", MaxParallel: &maxParallel})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetSpec(ctx, p.ID, "# The plan"); err != nil {
		t.Fatal(err)
	}
	for _, sp := range specs {
		if _, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{
			Key: sp.key, Title: "Title " + sp.key, Description: "Do " + sp.key, DependsOn: sp.deps, SparsePaths: sp.sparse,
		}); err != nil {
			t.Fatalf("add %s: %v", sp.key, err)
		}
	}
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	return p.ID
}

func milestones(t *testing.T, f fixture, id domain.PlanID) map[string]domain.Milestone {
	t.Helper()
	d, err := f.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]domain.Milestone{}
	for _, v := range d.Milestones {
		out[v.Milestone.Key] = v.Milestone
	}
	return out
}

func tick(t *testing.T, f fixture) {
	t.Helper()
	if err := f.svc.Tick(context.Background(), time.Now()); err != nil {
		t.Fatalf("tick: %v", err)
	}
}

// reportDone delivers a done report from the milestone's session, as
// `ao report --done` would.
func reportDone(t *testing.T, f fixture, id domain.PlanID, key, note string) {
	t.Helper()
	m := milestones(t, f, id)[key]
	if m.SessionID == "" {
		t.Fatalf("%s has no session", key)
	}
	f.svc.OnReport(context.Background(), domain.ReportRecord{SessionID: m.SessionID, State: domain.ReportDone, Note: note})
	tick(t, f)
}

func spawnedKeys(f fixture) []string {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	var keys []string
	for _, cfg := range f.sessions.spawns {
		if cfg.PlanContext != nil {
			keys = append(keys, cfg.PlanContext.MilestoneKey)
		}
	}
	return keys
}

func lastSpawn(f fixture) ports.SpawnConfig {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	return f.sessions.spawns[len(f.sessions.spawns)-1]
}

func statuses(t *testing.T, f fixture, id domain.PlanID) string {
	t.Helper()
	ms := milestones(t, f, id)
	var parts []string
	for _, key := range []string{"m1", "m2", "m3", "qa"} {
		if m, ok := ms[key]; ok {
			parts = append(parts, key+"="+string(m.Status))
		}
	}
	return strings.Join(parts, " ")
}

// (a) independent milestones run in parallel; a dependent waits for both and
// its prompt names both dependency branches.
func TestSchedulerRunsIndependentMilestonesFirstAndDependentsAfter(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2"}, msSpec{key: "m3", deps: []string{"m1", "m2"}})
	if got := strings.Join(spawnedKeys(f), ","); got != "m1,m2" {
		t.Fatalf("first tick spawned %s", got)
	}
	if got := statuses(t, f, id); got != "m1=running m2=running m3=pending qa=pending" {
		t.Fatalf("statuses = %s", got)
	}
	first := f.sessions.spawns[0]
	if first.Kind != domain.KindWorker || first.ParentSessionID != "orch-1" || first.CreatedBy != domain.SessionCreatedByOrchestrator ||
		!first.Async || first.RequestedMode != "" || first.ProjectID != "mer" || first.Branch != "" || first.BaseBranch != "" {
		t.Fatalf("spawn config = %+v", first)
	}
	m1 := milestones(t, f, id)["m1"]
	if first.ClientRequestID != "plan:"+string(m1.ID)+":1" || first.ClientRequestHash == "" {
		t.Fatalf("client request = %q / %q", first.ClientRequestID, first.ClientRequestHash)
	}
	if pc := first.PlanContext; pc == nil || pc.PlanID != id || pc.MilestoneID != m1.ID || pc.MilestoneKey != "m1" ||
		pc.Role != "implementer" || !strings.Contains(pc.PromptSection, string(id)) {
		t.Fatalf("plan context = %+v", first.PlanContext)
	}
	if !strings.Contains(first.DisplayName, "Todo · m1: Title m1") || !strings.Contains(first.Prompt, "# Milestone m1: Title m1") {
		t.Fatalf("display %q prompt:\n%s", first.DisplayName, first.Prompt)
	}

	reportDone(t, f, id, "m1", "built m1")
	if f.sessions.spawnCount() != 2 {
		t.Fatalf("m3 spawned before m2 was done: %v", spawnedKeys(f))
	}
	reportDone(t, f, id, "m2", "built m2")
	if got := strings.Join(spawnedKeys(f), ","); got != "m1,m2,m3" {
		t.Fatalf("spawned %s", got)
	}
	m3 := lastSpawn(f)
	for _, want := range []string{"`ao/w-1/root`", "`ao/w-2/root`", "git merge ao/w-1/root", "git merge ao/w-2/root"} {
		if !strings.Contains(m3.Prompt, want) {
			t.Fatalf("m3 prompt missing %q:\n%s", want, m3.Prompt)
		}
	}
	if m3.BaseBranch != "" {
		t.Fatalf("multi-dependency milestone stacked on %q", m3.BaseBranch)
	}
}

// (b) the parallel cap holds; the next ready milestone starts when a slot frees.
func TestSchedulerRespectsMaxParallel(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 2, msSpec{key: "m1"}, msSpec{key: "m2"}, msSpec{key: "m3"})
	if got := strings.Join(spawnedKeys(f), ","); got != "m1,m2" {
		t.Fatalf("cap 2 spawned %s", got)
	}
	if got := statuses(t, f, id); got != "m1=running m2=running m3=ready qa=pending" {
		t.Fatalf("statuses = %s", got)
	}
	tick(t, f)
	if f.sessions.spawnCount() != 2 {
		t.Fatal("cap exceeded on a later tick")
	}
	reportDone(t, f, id, "m1", "ok")
	if got := strings.Join(spawnedKeys(f), ","); got != "m1,m2,m3" {
		t.Fatalf("after a slot freed spawned %s", got)
	}
}

// (c) and Review Focus 2: single-dependency milestones stack on the
// dependency's branch with a generated branch of their own, so two siblings
// of one dependency both start.
func TestSchedulerStacksSingleDependencyMilestones(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2", deps: []string{"m1"}}, msSpec{key: "m3", deps: []string{"m1"}})
	reportDone(t, f, id, "m1", "ok")
	if got := strings.Join(spawnedKeys(f), ","); got != "m1,m2,m3" {
		t.Fatalf("spawned %s", got)
	}
	for _, cfg := range f.sessions.spawns[1:] {
		if cfg.BaseBranch != "ao/w-1/root" || cfg.Branch != "" || cfg.RequireNewBranch {
			t.Fatalf("%s: base=%q branch=%q", cfg.PlanContext.MilestoneKey, cfg.BaseBranch, cfg.Branch)
		}
	}
	if got := statuses(t, f, id); got != "m1=done m2=running m3=running qa=pending" {
		t.Fatalf("statuses = %s", got)
	}
}

// (d) QA spawns once every implement milestone is done, with a full checkout,
// outside the parallel cap.
func TestSchedulerSpawnsQAWhenImplementationIsDone(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{SparseCheckout: domain.SparseCheckoutConfig{Enabled: true}})
	id := activePlan(t, f, 1, msSpec{key: "m1", sparse: []string{"src"}})
	if got := lastSpawn(f).SparsePaths; len(got) != 1 || got[0] != "src" {
		t.Fatalf("implementer sparse = %v", got)
	}
	reportDone(t, f, id, "m1", "built the API, tests pass")
	qa := lastSpawn(f)
	if qa.PlanContext == nil || qa.PlanContext.Role != "qa" || qa.PlanContext.MilestoneKey != "qa" || len(qa.SparsePaths) != 0 ||
		qa.DisplayName != "Todo · QA" || qa.BaseBranch != "" {
		t.Fatalf("qa spawn = %+v", qa)
	}
	for _, want := range []string{"# QA for Task \"Todo\"", "m1: Title m1", "branch `ao/w-1/root`", "built the API, tests pass",
		"ao plan qa-result " + string(id) + " --fail --milestone <key>", "ao plan qa-result " + string(id) + " --pass", "# The plan"} {
		if !strings.Contains(qa.Prompt, want) {
			t.Fatalf("qa prompt missing %q:\n%s", want, qa.Prompt)
		}
	}
	if d, _ := f.svc.Get(context.Background(), id); d.Phase != domain.PlanPhaseQA {
		t.Fatalf("phase = %s", d.Phase)
	}
}

// (e) a worker that ends without reporting done fails and is re-spawned, up
// to three attempts, after which the plan needs a person.
func TestSchedulerRetriesEndedWorkersUpToThreeAttempts(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	for attempt := 1; attempt <= 3; attempt++ {
		m1 := milestones(t, f, id)["m1"]
		if m1.Status != domain.MilestoneStatusRunning || m1.Attempt != attempt {
			t.Fatalf("attempt %d: m1 = %s attempt %d", attempt, m1.Status, m1.Attempt)
		}
		if got := lastSpawn(f).ClientRequestID; got != fmt.Sprintf("plan:%s:%d", m1.ID, attempt) {
			t.Fatalf("client request id = %s", got)
		}
		f.sessions.endSession(m1.SessionID)
		tick(t, f)
	}
	m1 := milestones(t, f, id)["m1"]
	if m1.Status != domain.MilestoneStatusFailed || f.sessions.spawnCount() != 3 {
		t.Fatalf("after 3 attempts m1 = %s, spawns = %d", m1.Status, f.sessions.spawnCount())
	}
	tick(t, f)
	if f.sessions.spawnCount() != 3 {
		t.Fatal("re-spawned past the attempt limit")
	}
	if d, _ := f.svc.Get(context.Background(), id); d.Phase != domain.PlanPhaseFailed {
		t.Fatalf("phase = %s", d.Phase)
	}
	if kinds := strings.Join(eventKinds(t, f, id), ","); strings.Count(kinds, "milestone_failed") != 3 {
		t.Fatalf("events = %s", kinds)
	}
}

func TestSchedulerTreatsExitedActivityAsEnded(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	m1 := milestones(t, f, id)["m1"]
	f.sessions.mu.Lock()
	s := f.sessions.sessions[m1.SessionID]
	s.Activity.State = domain.ActivityExited
	f.sessions.sessions[m1.SessionID] = s
	f.sessions.mu.Unlock()
	tick(t, f)
	if m := milestones(t, f, id)["m1"]; m.Attempt != 2 || m.SessionID == m1.SessionID {
		t.Fatalf("exited worker not replaced: %+v", m)
	}
	// Idle is not done and not failed.
	f2 := flowFixture(t, domain.ProjectConfig{})
	id2 := activePlan(t, f2, 3, msSpec{key: "m1"})
	m := milestones(t, f2, id2)["m1"]
	f2.sessions.mu.Lock()
	s = f2.sessions.sessions[m.SessionID]
	s.Activity.State = domain.ActivityIdle
	f2.sessions.sessions[m.SessionID] = s
	f2.sessions.mu.Unlock()
	tick(t, f2)
	if got := milestones(t, f2, id2)["m1"]; got.Status != domain.MilestoneStatusRunning || got.Attempt != 1 {
		t.Fatalf("idle worker changed state: %+v", got)
	}
}

// (f) a spawn error fails the attempt and the next tick retries.
func TestSchedulerRetriesSpawnErrors(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	f.sessions.spawnErr = errors.New("agent harness required")
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	m1 := milestones(t, f, id)["m1"]
	if m1.Status != domain.MilestoneStatusFailed || m1.Attempt != 1 {
		t.Fatalf("after failed spawn m1 = %+v", m1)
	}
	evs, _ := f.store.ListPlanEvents(context.Background(), id, 0, 100)
	if last := evs[len(evs)-1]; last.Kind != "milestone_failed" || !strings.Contains(last.Note, "agent harness required") {
		t.Fatalf("last event = %+v", last)
	}
	f.sessions.mu.Lock()
	f.sessions.spawnErr = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusRunning || m.Attempt != 2 {
		t.Fatalf("retry m1 = %+v", m)
	}
}

// (g) and Review Focus 5: a plan cancelled while a spawn is in flight records
// the spawned session on its milestone, marks it cancelled, and starts nothing
// more.
func TestSchedulerStopsWhenThePlanIsCancelledMidTick(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	var planID domain.PlanID
	f.sessions.spawnHook = func(ports.SpawnConfig) {
		f.sessions.mu.Lock()
		f.sessions.spawnHook = nil
		f.sessions.mu.Unlock()
		if _, err := f.svc.Cancel(ctx, planID); err != nil {
			t.Errorf("cancel: %v", err)
		}
	}
	p, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "Todo", Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	planID = p.ID
	for _, key := range []string{"m1", "m2"} {
		if _, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: key, Title: key, Description: "d"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if f.sessions.spawnCount() != 1 {
		t.Fatalf("spawned %d after cancel", f.sessions.spawnCount())
	}
	ms := milestones(t, f, p.ID)
	if ms["m1"].Status != domain.MilestoneStatusCancelled || ms["m1"].SessionID != "w-1" || ms["m2"].Status != domain.MilestoneStatusCancelled {
		t.Fatalf("milestones = %+v / %+v", ms["m1"], ms["m2"])
	}
	tick(t, f)
	if f.sessions.spawnCount() != 1 {
		t.Fatal("a cancelled plan kept spawning")
	}
}

// (h) a project without sparse checkouts runs sparse milestones as full
// checkouts and says so once.
func TestSchedulerFallsBackToFullCheckoutWhenSparseIsDisabled(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1", sparse: []string{"a"}}, msSpec{key: "m2", sparse: []string{"b"}})
	for _, cfg := range f.sessions.spawns {
		if len(cfg.SparsePaths) != 0 {
			t.Fatalf("%s got sparse paths %v", cfg.PlanContext.MilestoneKey, cfg.SparsePaths)
		}
	}
	if n := strings.Count(strings.Join(eventKinds(t, f, id), ","), "sparse_disabled"); n != 1 {
		t.Fatalf("sparse_disabled events = %d", n)
	}
}

// A dependency whose session has no branch is named in the prompt with the
// way to ask for it.
func TestSchedulerNamesMissingDependencyBranches(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2", deps: []string{"m1"}})
	m1 := milestones(t, f, id)["m1"]
	f.sessions.mu.Lock()
	s := f.sessions.sessions[m1.SessionID]
	s.Metadata.Branch = ""
	f.sessions.sessions[m1.SessionID] = s
	f.sessions.mu.Unlock()
	reportDone(t, f, id, "m1", "ok")
	m2 := lastSpawn(f)
	if m2.BaseBranch != "" || !strings.Contains(m2.Prompt, "has no recorded branch") || !strings.Contains(m2.Prompt, "ao send") {
		t.Fatalf("m2 base=%q prompt:\n%s", m2.BaseBranch, m2.Prompt)
	}
}
