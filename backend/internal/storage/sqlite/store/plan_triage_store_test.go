package store_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPlanIntakeAndTriageRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	at := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)

	legacy := samplePlan("plan_old", "mer", at)
	legacy.IntakeMode = ""
	if err := s.InsertPlan(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GetPlan(ctx, legacy.ID)
	if err != nil || got.IntakeMode != domain.PlanIntakePlan || got.Triage != nil {
		t.Fatalf("plan without a mode = %+v err=%v, want intake plan and no triage", got, err)
	}

	auto := samplePlan("plan_auto", "mer", at)
	auto.State, auto.IntakeMode = domain.PlanStateTriage, domain.PlanIntakeAuto
	if err := s.InsertPlan(ctx, auto); err != nil {
		t.Fatal(err)
	}
	reminded := at.Add(15 * time.Minute)
	auto.TriageRemindedAt = &reminded
	auto.UpdatedAt = reminded
	if err := s.UpdatePlan(ctx, auto); err != nil {
		t.Fatal(err)
	}

	decided := auto
	finished := at.Add(20 * time.Minute)
	decided.State = domain.PlanStateDirect
	decided.DirectSessionID = "mer-7"
	decided.Triage = &domain.PlanTriage{Decision: domain.TriageDecisionDirect, Reason: "one small change", DecidedBy: domain.TriageActorHuman, DecidedAt: finished}
	decided.FinishedAt = &finished
	decided.UpdatedAt = finished
	won, err := s.DecidePlanTriage(ctx, decided)
	if err != nil || !won {
		t.Fatalf("first decision won=%v err=%v", won, err)
	}
	got, _, err = s.GetPlan(ctx, auto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.PlanStateDirect || got.DirectSessionID != "mer-7" || got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Fatalf("decided plan = %+v", got)
	}
	if !reflect.DeepEqual(got.Triage, decided.Triage) {
		t.Fatalf("triage = %+v, want %+v", got.Triage, decided.Triage)
	}
	if got.TriageRemindedAt == nil || !got.TriageRemindedAt.Equal(reminded) {
		t.Fatalf("reminded at = %v, want %s", got.TriageRemindedAt, reminded)
	}

	again := decided
	again.State = domain.PlanStatePlanning
	again.Triage = &domain.PlanTriage{Decision: domain.TriageDecisionPlan, DecidedBy: domain.TriageActorOrchestrator, DecidedAt: finished}
	if won, err := s.DecidePlanTriage(ctx, again); err != nil || won {
		t.Fatalf("second decision won=%v err=%v, want no write once decided", won, err)
	}
	if got, _, _ := s.GetPlan(ctx, auto.ID); got.State != domain.PlanStateDirect {
		t.Fatalf("second decision overwrote the first: state %s", got.State)
	}
}

func TestAutomationTaskModeAndPlanBackedRun(t *testing.T) {
	store := newTestStore(t)
	seedProject(t, store, "scheduled")
	ctx := context.Background()
	base := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)
	if _, err := store.CreateAutomation(ctx, domain.Automation{
		ID: "automation-1", ProjectID: "scheduled", DisplayName: "Triage", Prompt: "Review",
		Kind: domain.KindWorker, TaskMode: domain.AutomationTaskAuto, RRuleText: "FREQ=HOURLY", Timezone: "UTC", Enabled: true,
		NextRunAt: base.Add(3 * time.Hour), CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	def, ok, err := store.GetAutomation(ctx, "automation-1")
	if err != nil || !ok || def.TaskMode != domain.AutomationTaskAuto {
		t.Fatalf("automation = %+v ok=%v err=%v, want task mode auto", def, ok, err)
	}
	def.TaskMode = domain.AutomationTaskPlan
	if _, err := store.UpdateAutomation(ctx, def); err != nil {
		t.Fatal(err)
	}
	if def, _, _ = store.GetAutomation(ctx, "automation-1"); def.TaskMode != domain.AutomationTaskPlan {
		t.Fatalf("updated task mode = %q, want plan", def.TaskMode)
	}

	plan := samplePlan("plan_run", "scheduled", base)
	if err := store.InsertPlan(ctx, plan); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateAutomationRun(ctx, domain.AutomationRun{
		ID: "run-1", AutomationID: "automation-1", ScheduledFor: base, Status: domain.AutomationRunPending, CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ClaimNextAutomationRun(ctx, base, base.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if ok, err := store.MarkAutomationRunPlan(ctx, "run-1", plan.ID, base); err != nil || !ok {
		t.Fatalf("mark plan ok=%v err=%v", ok, err)
	}
	runs, err := store.ListActiveAutomationRuns(ctx)
	if err != nil || len(runs) != 1 || runs[0].Status != domain.AutomationRunRunning || runs[0].PlanID == nil || *runs[0].PlanID != plan.ID || runs[0].SessionID != nil {
		t.Fatalf("active runs = %+v err=%v, want one running run on the plan without a session", runs, err)
	}
	worker, err := store.CreateSession(ctx, sampleRecord("scheduled"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.AdoptAutomationRunSession(ctx, "run-1", worker.ID, base); err != nil || !ok {
		t.Fatalf("adopt ok=%v err=%v", ok, err)
	}
	if ok, err := store.AdoptAutomationRunSession(ctx, "run-1", worker.ID, base); err != nil || ok {
		t.Fatalf("second adopt ok=%v err=%v, want no write", ok, err)
	}
	run, ok, err := store.GetAutomationRun(ctx, "run-1")
	if err != nil || !ok || run.SessionID == nil || *run.SessionID != worker.ID || run.PlanID == nil {
		t.Fatalf("adopted run = %+v ok=%v err=%v", run, ok, err)
	}
}
