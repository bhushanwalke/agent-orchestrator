package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	plansvc "github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

func (f *schedulerMemoryStore) MarkAutomationRunPlan(_ context.Context, id domain.AutomationRunID, planID domain.PlanID, now time.Time) (bool, error) {
	run := f.runs[id]
	if run.Status != domain.AutomationRunSpawning {
		return false, nil
	}
	run.Status, run.PlanID, run.StartedAt, run.LeaseExpiresAt, run.UpdatedAt = domain.AutomationRunRunning, &planID, &now, nil, now
	f.runs[id] = run
	return true, nil
}

func (f *schedulerMemoryStore) AdoptAutomationRunSession(_ context.Context, id domain.AutomationRunID, sessionID domain.SessionID, now time.Time) (bool, error) {
	run := f.runs[id]
	if run.Status != domain.AutomationRunRunning || run.SessionID != nil {
		return false, nil
	}
	run.SessionID, run.UpdatedAt = &sessionID, now
	f.runs[id] = run
	return true, nil
}

type fakePlans struct {
	creates []plansvc.CreateInput
	err     error
	details map[domain.PlanID]plansvc.Detail
}

func (f *fakePlans) Create(_ context.Context, in plansvc.CreateInput) (domain.Plan, error) {
	f.creates = append(f.creates, in)
	if f.err != nil {
		return domain.Plan{}, f.err
	}
	return domain.Plan{ID: "plan_1", ProjectID: in.ProjectID, Title: in.Title}, nil
}

func (f *fakePlans) Get(_ context.Context, id domain.PlanID) (plansvc.Detail, error) {
	d, ok := f.details[id]
	if !ok {
		return plansvc.Detail{}, apierr.NotFound("PLAN_NOT_FOUND", "Unknown plan")
	}
	return d, nil
}

func planAutomation(mode domain.AutomationTaskMode, now time.Time) domain.Automation {
	return domain.Automation{ID: "automation-1", ProjectID: "scheduled", DisplayName: "Nightly cleanup", Prompt: "Tidy the flaky tests", Kind: domain.KindWorker,
		TaskMode: mode, RRuleText: "DTSTART:20261114T020000Z\nRRULE:FREQ=DAILY", Timezone: "Asia/Calcutta", Enabled: true, NextRunAt: now}
}

func TestTickDispatchesPlanAndAutoRunsThroughThePlanService(t *testing.T) {
	for _, mode := range []domain.AutomationTaskMode{domain.AutomationTaskPlan, domain.AutomationTaskAuto} {
		t.Run(string(mode), func(t *testing.T) {
			now := time.Date(2026, time.November, 14, 2, 0, 0, 0, time.UTC)
			store := newSchedulerStore()
			store.automations["automation-1"] = planAutomation(mode, now)
			spawner := &recordingSpawner{store: store}
			plans := &fakePlans{}
			svc := New(Deps{Store: store, Spawner: spawner, Plans: plans, Clock: func() time.Time { return now }})
			if err := svc.Tick(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			if len(spawner.calls) != 0 || len(plans.creates) != 1 {
				t.Fatalf("spawns=%d plan creates=%d, want 0 and 1", len(spawner.calls), len(plans.creates))
			}
			in := plans.creates[0]
			wantMode := domain.PlanIntakePlan
			if mode == domain.AutomationTaskAuto {
				wantMode = domain.PlanIntakeAuto
			}
			// 02:00 UTC is 07:30 in Asia/Calcutta.
			if in.ProjectID != "scheduled" || in.Title != "Nightly cleanup · 2026-11-14 07:30" || in.Brief != "Tidy the flaky tests" || in.Mode != wantMode || in.MaxParallel != nil {
				t.Fatalf("plan create = %+v", in)
			}
			var run domain.AutomationRun
			for _, r := range store.runs {
				run = r
			}
			if run.Status != domain.AutomationRunRunning || run.PlanID == nil || *run.PlanID != "plan_1" || run.SessionID != nil {
				t.Fatalf("run = %+v, want running on plan_1 with no session", run)
			}
		})
	}
}

func TestPlanRunTitleIsClippedToThePlanTitleLimit(t *testing.T) {
	def := planAutomation(domain.AutomationTaskPlan, time.Time{})
	def.DisplayName = strings.Repeat("n", 120)
	got := planRunTitle(def, time.Date(2026, time.November, 14, 2, 0, 0, 0, time.UTC))
	if n := len([]rune(got)); n != domain.PlanTitleMaxChars {
		t.Fatalf("title has %d runes, want %d: %q", n, domain.PlanTitleMaxChars, got)
	}
	if !strings.HasSuffix(got, "… · 2026-11-14 07:30") {
		t.Fatalf("clipped title = %q, want the date kept", got)
	}
}

func TestPlanRunWithoutPlanServiceFails(t *testing.T) {
	now := time.Date(2026, time.November, 14, 2, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = planAutomation(domain.AutomationTaskAuto, now)
	svc := New(Deps{Store: store, Spawner: &recordingSpawner{store: store}, Clock: func() time.Time { return now }})
	_ = svc.Tick(context.Background(), now)
	for _, run := range store.runs {
		if run.Status != domain.AutomationRunFailed || run.ErrorMessage != "plan service is unavailable" {
			t.Fatalf("run = %+v, want failed: plan service is unavailable", run)
		}
	}
}

func TestPlanRunCreateErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status domain.AutomationRunStatus
	}{
		{"invalid fails the run", apierr.Invalid("INVALID_PLAN", "Brief is required", nil), domain.AutomationRunFailed},
		{"transient releases the claim", errors.New("database is locked"), domain.AutomationRunPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, time.November, 14, 2, 0, 0, 0, time.UTC)
			store := newSchedulerStore()
			store.automations["automation-1"] = planAutomation(domain.AutomationTaskPlan, now)
			svc := New(Deps{Store: store, Spawner: &recordingSpawner{store: store}, Plans: &fakePlans{err: tc.err}, Clock: func() time.Time { return now }})
			_ = svc.Tick(context.Background(), now)
			for _, run := range store.runs {
				if run.Status != tc.status || run.PlanID != nil {
					t.Fatalf("run = %+v, want %s without a plan", run, tc.status)
				}
			}
		})
	}
}

func TestPlanBackedRunCompletesFromThePlanPhase(t *testing.T) {
	for _, tc := range []struct {
		phase   domain.PlanPhase
		status  domain.AutomationRunStatus
		message string
	}{
		{domain.PlanPhaseDone, domain.AutomationRunCompleted, ""},
		{domain.PlanPhaseFailed, domain.AutomationRunFailed, "failed"},
		{domain.PlanPhaseCancelled, domain.AutomationRunFailed, "cancelled"},
		{domain.PlanPhaseImplementing, domain.AutomationRunRunning, ""},
		{domain.PlanPhaseTriage, domain.AutomationRunRunning, ""},
	} {
		t.Run(string(tc.phase), func(t *testing.T) {
			now := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)
			store := newSchedulerStore()
			store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
			planID := domain.PlanID("plan_1")
			store.runs["run-1"] = domain.AutomationRun{ID: "run-1", AutomationID: "automation-1", ScheduledFor: now, PlanID: &planID, Status: domain.AutomationRunRunning}
			plans := &fakePlans{details: map[domain.PlanID]plansvc.Detail{planID: {Summary: plansvc.Summary{Plan: domain.Plan{ID: planID}, Phase: tc.phase}}}}
			svc := New(Deps{Store: store, Spawner: &recordingSpawner{store: store}, Plans: plans, Clock: func() time.Time { return now }})
			if err := svc.Tick(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			run := store.runs["run-1"]
			if run.Status != tc.status || run.ErrorMessage != tc.message {
				t.Fatalf("run = %+v, want %s %q", run, tc.status, tc.message)
			}
		})
	}
}

func TestDirectPlanRunAdoptsTheWorkerSession(t *testing.T) {
	now := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	planID := domain.PlanID("plan_1")
	store.runs["run-1"] = domain.AutomationRun{ID: "run-1", AutomationID: "automation-1", ScheduledFor: now, PlanID: &planID, Status: domain.AutomationRunRunning}
	store.sessions["scheduled-7"] = domain.SessionRecord{ID: "scheduled-7"}
	plans := &fakePlans{details: map[domain.PlanID]plansvc.Detail{planID: {Summary: plansvc.Summary{Plan: domain.Plan{ID: planID, DirectSessionID: "scheduled-7"}, Phase: domain.PlanPhaseDirect}}}}
	svc := New(Deps{Store: store, Spawner: &recordingSpawner{store: store}, Plans: plans, Clock: func() time.Time { return now }})
	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	run := store.runs["run-1"]
	if run.Status != domain.AutomationRunRunning || run.SessionID == nil || *run.SessionID != "scheduled-7" || run.PlanID == nil {
		t.Fatalf("run = %+v, want running on the adopted worker", run)
	}
	// From here the session-backed path finishes it, as for any worker run.
	rec := store.sessions["scheduled-7"]
	rec.IsTerminated = true
	store.sessions["scheduled-7"] = rec
	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if got := store.runs["run-1"].Status; got != domain.AutomationRunCompleted {
		t.Fatalf("run after the worker ended = %s, want completed", got)
	}
}

func TestPlanBackedRunWithMissingPlanFails(t *testing.T) {
	now := time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Enabled: true, NextRunAt: now.Add(time.Hour)}
	planID := domain.PlanID("plan_gone")
	store.runs["run-1"] = domain.AutomationRun{ID: "run-1", AutomationID: "automation-1", ScheduledFor: now, PlanID: &planID, Status: domain.AutomationRunRunning}
	svc := New(Deps{Store: store, Spawner: &recordingSpawner{store: store}, Plans: &fakePlans{}, Clock: func() time.Time { return now }})
	_ = svc.Tick(context.Background(), now)
	if run := store.runs["run-1"]; run.Status != domain.AutomationRunFailed || run.ErrorMessage != "Linked plan is missing" {
		t.Fatalf("run = %+v", run)
	}
}

func TestTaskModeValidation(t *testing.T) {
	store := newFakeStore()
	store.projects["scheduled"] = domain.ProjectRecord{ID: "scheduled"}
	svc := New(Deps{Store: store, Clock: func() time.Time { return time.Date(2026, time.November, 14, 9, 0, 0, 0, time.UTC) }})
	base := CreateInput{ProjectID: "scheduled", DisplayName: "A", Prompt: "p", Kind: domain.KindWorker, RRule: "FREQ=DAILY", Timezone: "UTC"}

	created, err := svc.Create(context.Background(), base)
	if err != nil || created.TaskMode != domain.AutomationTaskDirect {
		t.Fatalf("default task mode = %q err=%v, want direct", created.TaskMode, err)
	}
	auto := base
	auto.TaskMode = domain.AutomationTaskAuto
	if created, err := svc.Create(context.Background(), auto); err != nil || created.TaskMode != domain.AutomationTaskAuto {
		t.Fatalf("auto = %q err=%v", created.TaskMode, err)
	}
	bad := base
	bad.TaskMode = "session"
	_, err = svc.Create(context.Background(), bad)
	assertCode(t, err, "INVALID_AUTOMATION_TASK_MODE")
	orch := base
	orch.Kind, orch.TaskMode = domain.KindOrchestrator, domain.AutomationTaskPlan
	_, err = svc.Create(context.Background(), orch)
	assertCode(t, err, "INVALID_AUTOMATION_TASK_MODE")

	plan := domain.AutomationTaskPlan
	updated, err := svc.Update(context.Background(), created.ID, UpdateInput{TaskMode: &plan})
	if err != nil || updated.TaskMode != domain.AutomationTaskPlan {
		t.Fatalf("update to plan = %q err=%v", updated.TaskMode, err)
	}
	orchKind := domain.KindOrchestrator
	_, err = svc.Update(context.Background(), created.ID, UpdateInput{Kind: &orchKind})
	assertCode(t, err, "INVALID_AUTOMATION_TASK_MODE")
	direct := domain.AutomationTaskDirect
	if updated, err := svc.Update(context.Background(), created.ID, UpdateInput{Kind: &orchKind, TaskMode: &direct}); err != nil || updated.Kind != domain.KindOrchestrator {
		t.Fatalf("orchestrator + direct = %+v err=%v", updated, err)
	}
}

func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
}
