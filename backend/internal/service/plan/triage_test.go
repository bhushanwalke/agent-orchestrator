package plan_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// clock is a settable time source for reminder tests.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTriageFixture(t *testing.T) (fixture, *clock) {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", DisplayName: "Mercury", Kind: domain.ProjectKindSingleRepo,
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)}
	sessions := &fakeSessions{sessions: map[domain.SessionID]domain.Session{}}
	sched := &fakeScheduler{}
	n := 0
	svc := plan.New(plan.Deps{
		Store: st, Sessions: sessions, Projects: st, Scheduler: sched, KickoffRetryInterval: -1,
		Now:   c.Now,
		NewID: func(prefix string) string { n++; return fmt.Sprintf("%s%d", prefix, n) },
	})
	return fixture{svc: svc, store: st, sessions: sessions, sched: sched}, c
}

func createAuto(t *testing.T, f fixture) domain.Plan {
	t.Helper()
	p, err := f.svc.Create(context.Background(), plan.CreateInput{
		ProjectID: "mer", Title: "Rename the panel", Brief: "  Rename the 'Coming up' heading to 'Due soon'.\n", TicketRef: "github:o/r#7",
		Mode: domain.PlanIntakeAuto,
	})
	if err != nil {
		t.Fatalf("create auto: %v", err)
	}
	return p
}

func TestCreateAutoStartsInTriageWithTriageKickoff(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	if p.State != domain.PlanStateTriage || p.IntakeMode != domain.PlanIntakeAuto {
		t.Fatalf("auto plan = %+v, want triage/auto", p)
	}
	if len(f.sessions.sent) != 1 {
		t.Fatalf("sent = %+v", f.sessions.sent)
	}
	msg := f.sessions.sent[0].Text
	for _, want := range []string{
		fmt.Sprintf(`<ao-plan-triage id="%s">`, p.ID), "project Mercury", "Title: Rename the panel", "Ticket: github:o/r#7",
		"Rename the 'Coming up' heading to 'Due soon'.",
		fmt.Sprintf("ao plan triage %s --direct --reason", p.ID), fmt.Sprintf("ao plan triage %s --plan --reason", p.ID),
		fmt.Sprintf("ao plan set-spec %s --file", p.ID), fmt.Sprintf("ao plan activate %s", p.ID), "</ao-plan-triage>",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("triage kickoff missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "<ao-plan-request") {
		t.Fatalf("triage kickoff carries a plan request:\n%s", msg)
	}
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,triage_requested,kickoff_sent" {
		t.Fatalf("events = %s", got)
	}
}

func TestCreatePlanModeIsUnchanged(t *testing.T) {
	f, _ := newTriageFixture(t)
	p, err := f.svc.Create(context.Background(), plan.CreateInput{ProjectID: "mer", Title: "T", Brief: "b", Mode: domain.PlanIntakePlan})
	if err != nil || p.State != domain.PlanStatePlanning || p.IntakeMode != domain.PlanIntakePlan {
		t.Fatalf("plan mode = %+v err=%v", p, err)
	}
	if _, err := f.svc.Create(context.Background(), plan.CreateInput{ProjectID: "mer", Title: "T", Brief: "b", Mode: "direct"}); err == nil {
		t.Fatal("mode direct created a plan")
	} else {
		wantCode(t, err, "INVALID_PLAN_MODE")
	}
}

func TestTriagePlanMovesToPlanning(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	got, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionPlan, "API and UI both change", domain.TriageActorOrchestrator)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.PlanStatePlanning || got.Triage == nil || got.Triage.Decision != domain.TriageDecisionPlan ||
		got.Triage.DecidedBy != domain.TriageActorOrchestrator || got.Triage.Reason != "API and UI both change" || got.FinishedAt != nil {
		t.Fatalf("decided plan = %+v triage=%+v", got, got.Triage)
	}
	if f.sessions.spawnCount() != 0 {
		t.Fatalf("a plan decision spawned %d workers", f.sessions.spawnCount())
	}
	kinds := eventKinds(t, f, p.ID)
	if kinds[len(kinds)-1] != plan.EventTriageDecided {
		t.Fatalf("events = %v, want triage_decided last", kinds)
	}
	// The plan is an ordinary planning plan now: milestones can be added.
	addMs(t, f, p.ID, "m1")
	// An orchestrator decision needs no follow-up message: it plans in the same turn.
	if len(f.sessions.sent) != 1 {
		t.Fatalf("sent after an orchestrator plan decision = %d, want only the kickoff", len(f.sessions.sent))
	}
}

func TestHumanPlanDecisionSendsThePlanRequest(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	if _, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionPlan, "", domain.TriageActorHuman); err != nil {
		t.Fatal(err)
	}
	last := f.sessions.sent[len(f.sessions.sent)-1].Text
	if !strings.Contains(last, fmt.Sprintf(`<ao-plan-request id="%s">`, p.ID)) {
		t.Fatalf("after a human plan decision the orchestrator got:\n%s", last)
	}
}

func TestTriageDirectSpawnsOneTaskTabWorker(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	got, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "one small rename", domain.TriageActorOrchestrator)
	if err != nil {
		t.Fatal(err)
	}
	if f.sessions.spawnCount() != 1 {
		t.Fatalf("spawns = %d, want 1", f.sessions.spawnCount())
	}
	cfg := f.sessions.spawns[0]
	wantPrompt := "Task: Rename the panel\n\nRename the 'Coming up' heading to 'Due soon'.\n\nTicket: github:o/r#7"
	if cfg.Prompt != wantPrompt {
		t.Fatalf("prompt = %q, want %q", cfg.Prompt, wantPrompt)
	}
	if cfg.ProjectID != "mer" || cfg.Kind != domain.KindWorker || cfg.DisplayName != "Rename the panel" || !cfg.DisplayNameExplicit || !cfg.KeepDisplayName || !cfg.NameFromPrompt || !cfg.Async {
		t.Fatalf("spawn config = %+v", cfg)
	}
	if cfg.PlanContext != nil || len(cfg.SparsePaths) != 0 || cfg.ParentSessionID != "" || cfg.CreatedBy != "" || cfg.Harness != "" || cfg.Branch != "" || cfg.BaseBranch != "" {
		t.Fatalf("direct worker carries plan or orchestrator links: %+v", cfg)
	}
	if got.State != domain.PlanStateDirect || got.DirectSessionID != "w-1" || got.FinishedAt == nil ||
		got.Triage == nil || got.Triage.Decision != domain.TriageDecisionDirect || got.Triage.Reason != "one small rename" {
		t.Fatalf("decided plan = %+v triage=%+v", got, got.Triage)
	}
	stored, _, _ := f.store.GetPlan(context.Background(), p.ID)
	if stored.State != domain.PlanStateDirect || stored.DirectSessionID != "w-1" {
		t.Fatalf("stored plan = %+v", stored)
	}
	kinds := eventKinds(t, f, p.ID)
	if got := strings.Join(kinds[len(kinds)-2:], ","); got != "triage_decided,direct_spawned" {
		t.Fatalf("events = %v", kinds)
	}
	d, err := f.svc.Get(context.Background(), p.ID)
	if err != nil || d.Phase != domain.PlanPhaseDirect {
		t.Fatalf("detail phase = %v err=%v", d.Phase, err)
	}
}

func TestSecondTriageIsRefusedWithoutSpawning(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	if _, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "", domain.TriageActorOrchestrator); err != nil {
		t.Fatal(err)
	}
	for _, d := range []domain.TriageDecision{domain.TriageDecisionDirect, domain.TriageDecisionPlan} {
		_, err := f.svc.Triage(context.Background(), p.ID, d, "", domain.TriageActorHuman)
		ae := wantCode(t, err, "PLAN_NOT_IN_TRIAGE")
		if ae.Kind != apierr.KindConflict {
			t.Fatalf("second triage kind = %v, want conflict", ae.Kind)
		}
	}
	if f.sessions.spawnCount() != 1 {
		t.Fatalf("spawns = %d after repeated decisions, want 1", f.sessions.spawnCount())
	}
}

func TestConcurrentDirectTriageSpawnsOnce(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "", domain.TriageActorHuman)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "PLAN_NOT_IN_TRIAGE")
		}
	}
	if ok != 1 || f.sessions.spawnCount() != 1 {
		t.Fatalf("successes = %d, spawns = %d, want 1 and 1", ok, f.sessions.spawnCount())
	}
}

func TestTriageValidation(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	_, err := f.svc.Triage(context.Background(), p.ID, "maybe", "", domain.TriageActorHuman)
	wantCode(t, err, "INVALID_TRIAGE_DECISION")
	_, err = f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionPlan, strings.Repeat("x", 501), domain.TriageActorHuman)
	wantCode(t, err, "INVALID_TRIAGE_REASON")
	_, err = f.svc.Triage(context.Background(), "plan_nope", domain.TriageDecisionPlan, "", domain.TriageActorHuman)
	wantCode(t, err, "PLAN_NOT_FOUND")

	planned := createPlan(t, f)
	_, err = f.svc.Triage(context.Background(), planned.ID, domain.TriageDecisionDirect, "", domain.TriageActorHuman)
	wantCode(t, err, "PLAN_NOT_IN_TRIAGE")
	if f.sessions.spawnCount() != 0 {
		t.Fatal("a refused decision spawned a worker")
	}
}

func TestDirectSpawnFailureKeepsTriageAndTellsTheOrchestrator(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	f.sessions.spawnErr = errors.New("worktree busy")
	if _, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "", domain.TriageActorOrchestrator); err == nil {
		t.Fatal("a failed direct spawn reported success")
	}
	stored, _, _ := f.store.GetPlan(context.Background(), p.ID)
	if stored.State != domain.PlanStateTriage || stored.Triage != nil || stored.DirectSessionID != "" {
		t.Fatalf("plan after failed spawn = %+v", stored)
	}
	kinds := eventKinds(t, f, p.ID)
	if kinds[len(kinds)-1] != plan.EventDirectSpawnFailed {
		t.Fatalf("events = %v, want direct_spawn_failed last", kinds)
	}
	last := f.sessions.sent[len(f.sessions.sent)-1]
	if last.ID != "orch-1" || !strings.Contains(last.Text, fmt.Sprintf(`<ao-plan-triage-failed id="%s">`, p.ID)) || !strings.Contains(last.Text, "worktree busy") {
		t.Fatalf("failure notice = %+v", last)
	}

	// The decision can be made again once the cause is fixed.
	f.sessions.spawnErr = nil
	if got, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "", domain.TriageActorOrchestrator); err != nil || got.State != domain.PlanStateDirect {
		t.Fatalf("retry = %+v err=%v", got, err)
	}
}

func TestTriageReminderIsSentOnceAfterFifteenMinutes(t *testing.T) {
	f, c := newTriageFixture(t)
	p := createAuto(t, f)
	tick := func() {
		t.Helper()
		if err := f.svc.Tick(context.Background(), c.Now()); err != nil {
			t.Fatal(err)
		}
	}
	reminders := func() int {
		n := 0
		for _, m := range f.sessions.sent {
			if strings.Contains(m.Text, "<ao-plan-triage-reminder") {
				n++
			}
		}
		return n
	}
	c.advance(14 * time.Minute)
	tick()
	if reminders() != 0 {
		t.Fatal("reminder sent before 15 minutes")
	}
	c.advance(time.Minute)
	tick()
	if reminders() != 1 {
		t.Fatalf("reminders at 15 minutes = %d, want 1", reminders())
	}
	msg := f.sessions.sent[len(f.sessions.sent)-1].Text
	if !strings.Contains(msg, fmt.Sprintf(`<ao-plan-triage-reminder id="%s">`, p.ID)) || !strings.Contains(msg, fmt.Sprintf("ao plan triage %s --direct", p.ID)) {
		t.Fatalf("reminder = %s", msg)
	}
	c.advance(time.Hour)
	tick()
	if reminders() != 1 {
		t.Fatalf("reminders after an hour = %d, want still 1", reminders())
	}
	stored, _, _ := f.store.GetPlan(context.Background(), p.ID)
	if stored.TriageRemindedAt == nil || stored.State != domain.PlanStateTriage {
		t.Fatalf("plan after reminder = %+v; no decision is ever made automatically", stored)
	}
	n := 0
	for _, k := range eventKinds(t, f, p.ID) {
		if k == plan.EventTriageReminder {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("triage_reminder events = %d, want 1", n)
	}
}

func TestRefusedReminderIsRetriedNextTick(t *testing.T) {
	f, c := newTriageFixture(t)
	createAuto(t, f)
	c.advance(16 * time.Minute)
	f.sessions.sendErr = errors.New("busy")
	_ = f.svc.Tick(context.Background(), c.Now())
	f.sessions.sendErr = nil
	_ = f.svc.Tick(context.Background(), c.Now())
	n := 0
	for _, m := range f.sessions.sent {
		if strings.Contains(m.Text, "<ao-plan-triage-reminder") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("reminders = %d, want 1 after the refused first try", n)
	}
}

func TestTriageKickoffDeferredIsRetriedByTick(t *testing.T) {
	f, c := newTriageFixture(t)
	f.sessions.sendErr = errors.New("starting")
	p := createAuto(t, f)
	f.sessions.sendErr = nil
	if err := f.svc.Tick(context.Background(), c.Now()); err != nil {
		t.Fatal(err)
	}
	if len(f.sessions.sent) != 1 || !strings.Contains(f.sessions.sent[0].Text, fmt.Sprintf(`<ao-plan-triage id="%s">`, p.ID)) {
		t.Fatalf("retried kickoff = %+v", f.sessions.sent)
	}
}

func TestDirectPlansAreNeverTickedOrCancelled(t *testing.T) {
	f, c := newTriageFixture(t)
	p := createAuto(t, f)
	if _, err := f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "", domain.TriageActorHuman); err != nil {
		t.Fatal(err)
	}
	before := len(f.sessions.sent)
	c.advance(time.Hour)
	if err := f.svc.Tick(context.Background(), c.Now()); err != nil {
		t.Fatal(err)
	}
	if len(f.sessions.sent) != before || f.sessions.spawnCount() != 1 {
		t.Fatalf("a direct plan was ticked: sent %d→%d, spawns %d", before, len(f.sessions.sent), f.sessions.spawnCount())
	}
	s, err := f.svc.Cancel(context.Background(), p.ID)
	if err != nil || s.Plan.State != domain.PlanStateDirect {
		t.Fatalf("cancel of a direct plan = %+v err=%v, want it unchanged", s.Plan, err)
	}
}

func TestCancelInTriage(t *testing.T) {
	f, _ := newTriageFixture(t)
	p := createAuto(t, f)
	s, err := f.svc.Cancel(context.Background(), p.ID)
	if err != nil || s.Plan.State != domain.PlanStateCancelled {
		t.Fatalf("cancel in triage = %+v err=%v", s.Plan, err)
	}
	_, err = f.svc.Triage(context.Background(), p.ID, domain.TriageDecisionDirect, "", domain.TriageActorHuman)
	wantCode(t, err, "PLAN_NOT_IN_TRIAGE")
}
