package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionBoardRequestRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	created, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	if ok, err := s.SetSessionBoardRequest(ctx, created.ID, domain.BoardRequestAccepted, at); err != nil || !ok {
		t.Fatalf("set: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetSession(ctx, created.ID)
	if err != nil || got.BoardRequest != domain.BoardRequestAccepted || got.BoardRequestAt == nil || !got.BoardRequestAt.Equal(at) {
		t.Fatalf("after set: request=%q at=%v err=%v", got.BoardRequest, got.BoardRequestAt, err)
	}
	all, err := s.ListAllSessions(ctx)
	if err != nil || len(all) != 1 || all[0].BoardRequest != domain.BoardRequestAccepted {
		t.Fatalf("list: %+v err=%v", all, err)
	}
	if ok, err := s.SetSessionBoardRequest(ctx, created.ID, domain.BoardRequestNone, at.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("clear: ok=%v err=%v", ok, err)
	}
	got, _, _ = s.GetSession(ctx, created.ID)
	if got.BoardRequest != domain.BoardRequestNone || got.BoardRequestAt != nil {
		t.Fatalf("after clear: request=%q at=%v", got.BoardRequest, got.BoardRequestAt)
	}
	// A plan-only request is refused by the column's check.
	if _, err := s.SetSessionBoardRequest(ctx, created.ID, domain.BoardRequestHold, at); err == nil {
		t.Fatal("a session accepted the plan-only hold request")
	}
	if ok, err := s.SetSessionBoardRequest(ctx, "missing", domain.BoardRequestAttention, at); err != nil || ok {
		t.Fatalf("missing session: ok=%v err=%v", ok, err)
	}
}

func TestPlanBoardRequestRoundTrips(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	seedProject(t, s, "mer")
	if err := s.InsertPlan(ctx, samplePlan("plan_1", "mer", at)); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	if ok, err := s.SetPlanBoardRequest(ctx, "plan_1", domain.BoardRequestHold, at); err != nil || !ok {
		t.Fatalf("set: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetPlan(ctx, "plan_1")
	if err != nil || got.BoardRequest != domain.BoardRequestHold || got.BoardRequestAt == nil {
		t.Fatalf("after set: %+v err=%v", got, err)
	}
	// A full-row plan update leaves the request alone.
	got.Title = "Renamed"
	if err := s.UpdatePlan(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _, _ = s.GetPlan(ctx, "plan_1")
	if got.BoardRequest != domain.BoardRequestHold {
		t.Fatalf("update cleared the request: %q", got.BoardRequest)
	}
	if _, err := s.SetPlanBoardRequest(ctx, "plan_1", domain.BoardRequestAccepted, at); err == nil {
		t.Fatal("a plan accepted the session-only accepted request")
	}
}

func TestManualAutomationRunIsRefusedWhileARunIsOpen(t *testing.T) {
	store := newTestStore(t)
	seedProject(t, store, "scheduled")
	ctx := context.Background()
	now := time.Date(2026, time.November, 15, 9, 0, 0, 0, time.UTC)
	if _, err := store.CreateAutomation(ctx, domain.Automation{ID: "automation-1", ProjectID: "scheduled", DisplayName: "Triage", Prompt: "Review", Kind: domain.KindWorker, RRuleText: "FREQ=DAILY", Timezone: "UTC", Enabled: true, NextRunAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run := domain.AutomationRun{ID: "run-1", AutomationID: "automation-1", ScheduledFor: now, Status: domain.AutomationRunPending, CreatedAt: now, UpdatedAt: now}
	if ok, err := store.InsertManualAutomationRun(ctx, run); err != nil || !ok {
		t.Fatalf("first: ok=%v err=%v", ok, err)
	}
	second := run
	second.ID, second.ScheduledFor = "run-2", now.Add(time.Second)
	if ok, err := store.InsertManualAutomationRun(ctx, second); err != nil || ok {
		t.Fatalf("second while pending: ok=%v err=%v", ok, err)
	}
	got, found, err := store.GetAutomationRun(ctx, "run-1")
	if err != nil || !found || got.Status != domain.AutomationRunPending {
		t.Fatalf("run-1 = %+v found=%v err=%v", got, found, err)
	}
}
