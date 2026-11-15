package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestRunNowQueuesAPendingRunTheSchedulerClaims(t *testing.T) {
	now := time.Date(2026, time.November, 15, 10, 0, 0, 0, time.UTC)
	next := now.Add(24 * time.Hour)
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", DisplayName: "Daily", Prompt: "Review", Kind: domain.KindWorker, RRuleText: "DTSTART:20261101T090000Z\nRRULE:FREQ=DAILY", Timezone: "UTC", Enabled: true, NextRunAt: next}
	spawner := &recordingSpawner{store: store}
	svc := New(Deps{Store: store, Spawner: spawner, Clock: func() time.Time { return now }, NewID: func() string { return "x" }})

	run, err := svc.RunNow(context.Background(), "automation-1")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if run.Status != domain.AutomationRunPending || !run.ScheduledFor.Equal(now) || store.runs[run.ID].Status != domain.AutomationRunPending {
		t.Fatalf("run = %+v", run)
	}
	select {
	case <-svc.WakeC():
	default:
		t.Fatal("run-now did not wake the scheduler")
	}
	_, err = svc.RunNow(context.Background(), "automation-1")
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "AUTOMATION_RUN_ACTIVE" {
		t.Fatalf("second RunNow err = %v, want AUTOMATION_RUN_ACTIVE", err)
	}
	if err := svc.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(spawner.calls) != 1 || spawner.calls[0].AutomationRunID == nil || *spawner.calls[0].AutomationRunID != run.ID {
		t.Fatalf("spawns = %+v, want the run-now run", spawner.calls)
	}
	if got := store.automations["automation-1"].NextRunAt; !got.Equal(next) {
		t.Fatalf("next run = %s, want the schedule unchanged at %s", got, next)
	}
}

func TestRunNowRefusesADisabledAutomation(t *testing.T) {
	store := newSchedulerStore()
	store.automations["automation-1"] = domain.Automation{ID: "automation-1", ProjectID: "scheduled", Kind: domain.KindWorker, Enabled: false}
	svc := New(Deps{Store: store})
	_, err := svc.RunNow(context.Background(), "automation-1")
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "AUTOMATION_DISABLED" {
		t.Fatalf("err = %v, want AUTOMATION_DISABLED", err)
	}
	if len(store.runs) != 0 {
		t.Fatalf("runs = %+v, want none", store.runs)
	}
}
