package plan_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func boardRefusal(t *testing.T, err error) string {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an API error", err)
	}
	reason, _ := ae.Details["reason"].(string)
	return reason
}

func sentWith(f fixture, tag string) []string {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	var out []string
	for _, m := range f.sessions.sent {
		if strings.HasPrefix(m.Text, tag) {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestBoardHoldPausesSpawningUntilReleased(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	p, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "Todo", Brief: "Build it"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SetSpec(ctx, p.ID, "# The plan"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "m1", Title: "One", Description: "Do one"}); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.BoardMove(ctx, p.ID, contract.KanbanQueued)
	if err != nil {
		t.Fatalf("hold: %v", err)
	}
	if d.Kanban != contract.KanbanQueued || d.DisplayStatus != contract.DisplayOnHold {
		t.Fatalf("held card = %s/%s", d.Kanban, d.DisplayStatus)
	}
	if hold := sentWith(f, "<ao-plan-hold>"); len(hold) != 1 || strings.Contains(hold[0], "two to five lines") {
		t.Fatalf("hold notices = %q", hold)
	}
	// The orchestrator activates the held Task: the decision is recorded,
	// but no worker spawns, on activation or on later ticks.
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	tick(t, f)
	if keys := spawnedKeys(f); len(keys) != 0 {
		t.Fatalf("spawned %v while held", keys)
	}
	d, err = f.svc.BoardMove(ctx, p.ID, contract.KanbanBuilding)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if d.Plan.BoardRequest != domain.BoardRequestNone {
		t.Fatalf("request after release = %q", d.Plan.BoardRequest)
	}
	tick(t, f)
	if keys := spawnedKeys(f); len(keys) != 1 || keys[0] != "m1" {
		t.Fatalf("spawned after release = %v, want [m1]", keys)
	}
	if resume := sentWith(f, "<ao-plan-resume>"); len(resume) != 1 {
		t.Fatalf("resume notices = %q", resume)
	}
	var notes []string
	evs, _ := f.store.ListPlanEvents(ctx, p.ID, 0, 100)
	for _, e := range evs {
		if e.Kind == plan.EventBoardMove {
			notes = append(notes, e.Note)
		}
	}
	if len(notes) != 2 || notes[0] != "hold → queued" || notes[1] != "released → building" {
		t.Fatalf("board_move events = %q", notes)
	}
}

func TestBoardHoldIsRefusedWhileAMilestoneRuns(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 1, msSpec{key: "m1"})
	if keys := spawnedKeys(f); len(keys) != 1 {
		t.Fatalf("spawned = %v", keys)
	}
	_, err := f.svc.BoardMove(context.Background(), id, contract.KanbanQueued)
	if got := boardRefusal(t, err); got != string(domain.BoardRefusalPlanNotLive) {
		t.Fatalf("refusal = %q", got)
	}
	// Needs you is allowed: running milestones keep going.
	d, err := f.svc.BoardMove(context.Background(), id, contract.KanbanNeedsYou)
	if err != nil || d.Kanban != contract.KanbanNeedsYou || d.DisplayStatus != contract.DisplayPausedForYou || d.KanbanStage != contract.KanbanBuilding {
		t.Fatalf("attention: %s/%s/%s err=%v", d.Kanban, d.DisplayStatus, d.KanbanStage, err)
	}
	if hold := sentWith(f, "<ao-plan-hold>"); len(hold) != 1 || !strings.Contains(hold[0], "two to five lines") {
		t.Fatalf("attention notice = %q", hold)
	}
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusRunning {
		t.Fatalf("m1 = %s, want still running", m.Status)
	}
}

func TestBoardMoveRefusesWhatQADecides(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	p := createPlan(t, f)
	for to, want := range map[contract.KanbanColumn]domain.BoardMoveRefusal{
		contract.KanbanValidating:  domain.BoardRefusalPlanCannotValidate,
		contract.KanbanNeedsReview: domain.BoardRefusalPlanCannotValidate,
		contract.KanbanReady:       domain.BoardRefusalPlanDecidesReady,
		contract.KanbanBuilding:    domain.BoardRefusalAlreadyThere,
	} {
		_, err := f.svc.BoardMove(context.Background(), p.ID, to)
		if got := boardRefusal(t, err); got != string(want) {
			t.Errorf("to %s: refusal = %q, want %q", to, got, want)
		}
	}
	_, err := f.svc.BoardMove(context.Background(), p.ID, contract.KanbanArchive)
	wantCode(t, err, "INVALID_BOARD_COLUMN")
	if _, err := f.svc.Cancel(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.BoardMove(context.Background(), p.ID, contract.KanbanQueued)
	wantCode(t, err, "PLAN_NOT_LIVE")
}

func TestTriageWhileHeldDefersTheDirectSpawn(t *testing.T) {
	f, c := newTriageFixture(t)
	ctx := context.Background()
	p := createAuto(t, f)
	if _, err := f.svc.BoardMove(ctx, p.ID, contract.KanbanQueued); err != nil {
		t.Fatalf("hold: %v", err)
	}
	got, err := f.svc.Triage(ctx, p.ID, domain.TriageDecisionDirect, "one small rename", domain.TriageActorHuman)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	if f.sessions.spawnCount() != 0 || got.State != domain.PlanStateTriage || got.Triage == nil {
		t.Fatalf("held direct decision spawned=%d plan=%+v", f.sessions.spawnCount(), got)
	}
	if _, err := f.svc.Triage(ctx, p.ID, domain.TriageDecisionPlan, "", domain.TriageActorHuman); err == nil {
		t.Fatal("a deferred decision was decided again")
	}
	// No reminder while held, however long it waits.
	c.advance(time.Hour)
	if err := f.svc.Tick(ctx, c.Now()); err != nil {
		t.Fatal(err)
	}
	if r := sentWith(f, "<ao-plan-triage-reminder"); len(r) != 0 {
		t.Fatalf("reminder sent while held: %q", r)
	}
	d, err := f.svc.BoardMove(ctx, p.ID, contract.KanbanBuilding)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if f.sessions.spawnCount() != 1 || d.Plan.State != domain.PlanStateDirect || d.Plan.DirectSessionID == "" {
		t.Fatalf("after release spawned=%d plan=%+v", f.sessions.spawnCount(), d.Plan)
	}
	kinds := strings.Join(eventKinds(t, f, p.ID), ",")
	for _, want := range []string{plan.EventBoardMove, plan.EventTriageDeferred} {
		if !strings.Contains(kinds, want) {
			t.Fatalf("events %s lack %s", kinds, want)
		}
	}
}

func TestTriageCardWaitsInQueueThenNeedsYou(t *testing.T) {
	f, c := newTriageFixture(t)
	p := createAuto(t, f)
	d, err := f.svc.Get(context.Background(), p.ID)
	if err != nil || d.Kanban != contract.KanbanQueued || d.DisplayStatus != contract.DisplayDeciding {
		t.Fatalf("new auto Task = %s/%s err=%v", d.Kanban, d.DisplayStatus, err)
	}
	c.advance(domain.TriageReminderAfter)
	d, _ = f.svc.Get(context.Background(), p.ID)
	if d.Kanban != contract.KanbanNeedsYou || d.DisplayStatus != contract.DisplayNeedsADecision || d.KanbanStage != contract.KanbanBuilding {
		t.Fatalf("after the reminder = %s/%s/%s", d.Kanban, d.DisplayStatus, d.KanbanStage)
	}
}
