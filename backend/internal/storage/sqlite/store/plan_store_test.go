package store_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func samplePlan(id, project string, at time.Time) domain.Plan {
	return domain.Plan{
		ID:          domain.PlanID(id),
		ProjectID:   domain.ProjectID(project),
		Title:       "Demo",
		Brief:       "Add X",
		TicketRef:   "github:o/r#1",
		State:       domain.PlanStatePlanning,
		MaxParallel: domain.DefaultPlanMaxParallel,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

func sampleMilestone(id, plan, key string, ordinal int, at time.Time) domain.Milestone {
	return domain.Milestone{
		ID:          domain.MilestoneID(id),
		PlanID:      domain.PlanID(plan),
		Key:         key,
		Ordinal:     ordinal,
		Kind:        domain.MilestoneKindImplement,
		Title:       "Milestone " + key,
		Description: "do " + key,
		Status:      domain.MilestoneStatusPending,
		SparsePaths: []string{},
		DependsOn:   []string{},
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

func seedPlanWithMilestones(t *testing.T, s *sqlite.Store, at time.Time) {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "mer")
	if err := s.InsertPlan(ctx, samplePlan("plan_1", "mer", at)); err != nil {
		t.Fatalf("insert plan: %v", err)
	}
	m1 := sampleMilestone("ms_1", "plan_1", "m1", 0, at)
	m1.SparsePaths = []string{"src/api", "src/shared"}
	m2 := sampleMilestone("ms_2", "plan_1", "m2", 1, at)
	m3 := sampleMilestone("ms_3", "plan_1", "m3", 2, at)
	for _, m := range []domain.Milestone{m1, m2, m3} {
		if err := s.InsertMilestone(ctx, m); err != nil {
			t.Fatalf("insert milestone %s: %v", m.Key, err)
		}
	}
	if err := s.SetMilestoneDeps(ctx, "ms_3", []domain.MilestoneID{"ms_1", "ms_2"}); err != nil {
		t.Fatalf("set deps: %v", err)
	}
}

func TestPlanRoundTripWithMilestonesAndDeps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	seedPlanWithMilestones(t, s, at)

	got, ok, err := s.GetPlan(ctx, "plan_1")
	if err != nil || !ok {
		t.Fatalf("get plan: ok=%v err=%v", ok, err)
	}
	if want := samplePlan("plan_1", "mer", at); !reflect.DeepEqual(got, want) {
		t.Fatalf("plan round trip:\n got %+v\nwant %+v", got, want)
	}

	activated := at.Add(time.Minute)
	got.State = domain.PlanStateActive
	got.ActivatedAt = &activated
	got.OrchestratorSessionID = "orch-1"
	got.SpecMarkdown = "# Plan"
	got.UpdatedAt = activated
	if err := s.UpdatePlan(ctx, got); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	again, _, _ := s.GetPlan(ctx, "plan_1")
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("updated plan:\n got %+v\nwant %+v", again, got)
	}
	active, err := s.ListActivePlans(ctx)
	if err != nil || len(active) != 1 || active[0].ID != "plan_1" {
		t.Fatalf("active plans = %+v err=%v", active, err)
	}
	listed, err := s.ListPlans(ctx, "mer")
	if err != nil || len(listed) != 1 {
		t.Fatalf("list plans = %+v err=%v", listed, err)
	}

	ms, err := s.ListMilestones(ctx, "plan_1")
	if err != nil {
		t.Fatalf("list milestones: %v", err)
	}
	if len(ms) != 3 || ms[0].Key != "m1" || ms[2].Key != "m3" {
		t.Fatalf("milestones = %+v", ms)
	}
	if !reflect.DeepEqual(ms[0].SparsePaths, []string{"src/api", "src/shared"}) {
		t.Fatalf("sparse paths = %#v", ms[0].SparsePaths)
	}
	if !reflect.DeepEqual(ms[2].DependsOn, []string{"m1", "m2"}) || len(ms[0].DependsOn) != 0 {
		t.Fatalf("deps m3=%#v m1=%#v", ms[2].DependsOn, ms[0].DependsOn)
	}

	if err := s.SetMilestoneDeps(ctx, "ms_3", []domain.MilestoneID{"ms_2"}); err != nil {
		t.Fatalf("replace deps: %v", err)
	}
	ms, _ = s.ListMilestones(ctx, "plan_1")
	if !reflect.DeepEqual(ms[2].DependsOn, []string{"m2"}) {
		t.Fatalf("replaced deps = %#v", ms[2].DependsOn)
	}

	dup := sampleMilestone("ms_9", "plan_1", "m1", 9, at)
	if err := s.InsertMilestone(ctx, dup); !errors.Is(err, domain.ErrMilestoneKeyExists) {
		t.Fatalf("duplicate key err = %v, want ErrMilestoneKeyExists", err)
	}

	if err := s.DeleteMilestone(ctx, "ms_2"); err != nil {
		t.Fatalf("delete milestone: %v", err)
	}
	ms, _ = s.ListMilestones(ctx, "plan_1")
	if len(ms) != 2 || len(ms[1].DependsOn) != 0 {
		t.Fatalf("after delete milestones = %+v", ms)
	}
}

func TestPlanGetMilestoneBySessionAndEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	seedPlanWithMilestones(t, s, at)

	if _, ok, err := s.GetMilestoneBySession(ctx, "sess-1"); err != nil || ok {
		t.Fatalf("unknown session: ok=%v err=%v", ok, err)
	}
	ms, _ := s.ListMilestones(ctx, "plan_1")
	m2 := ms[1]
	started := at.Add(time.Second)
	m2.Status = domain.MilestoneStatusRunning
	m2.SessionID = "sess-1"
	m2.Attempt = 1
	m2.StartedAt = &started
	m2.UpdatedAt = started
	if err := s.UpdateMilestone(ctx, m2); err != nil {
		t.Fatalf("update milestone: %v", err)
	}
	got, ok, err := s.GetMilestoneBySession(ctx, "sess-1")
	if err != nil || !ok || got.ID != "ms_2" || got.Status != domain.MilestoneStatusRunning || got.Attempt != 1 ||
		got.StartedAt == nil || !got.StartedAt.Equal(started) {
		t.Fatalf("by session = %+v ok=%v err=%v", got, ok, err)
	}

	for i, kind := range []string{"plan_created", "milestone_added", "plan_activated"} {
		if err := s.InsertPlanEvent(ctx, domain.PlanEvent{
			PlanID: "plan_1", MilestoneID: "ms_2", SessionID: "sess-1", Kind: kind, Note: "n",
			CreatedAt: at.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("insert event: %v", err)
		}
	}
	evs, err := s.ListPlanEvents(ctx, "plan_1", 0, 10)
	if err != nil || len(evs) != 3 || evs[0].Kind != "plan_created" || evs[2].Kind != "plan_activated" {
		t.Fatalf("events = %+v err=%v", evs, err)
	}
	after, _ := s.ListPlanEvents(ctx, "plan_1", evs[0].ID, 1)
	if len(after) != 1 || after[0].Kind != "milestone_added" || after[0].MilestoneID != "ms_2" {
		t.Fatalf("events after = %+v", after)
	}
	recent, err := s.ListRecentPlanEvents(ctx, "plan_1", 2)
	if err != nil || len(recent) != 2 || recent[0].Kind != "plan_activated" {
		t.Fatalf("recent events = %+v err=%v", recent, err)
	}
}

func TestPlanMilestoneUpdateWritesPlanUpdatedChangeLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	seedPlanWithMilestones(t, s, at)

	before, err := s.EventsAfter(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := s.ListMilestones(ctx, "plan_1")
	ms[0].Status = domain.MilestoneStatusReady
	ms[0].UpdatedAt = at.Add(time.Second)
	if err := s.UpdateMilestone(ctx, ms[0]); err != nil {
		t.Fatalf("update milestone: %v", err)
	}
	evs, err := s.EventsAfter(ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != len(before)+1 {
		t.Fatalf("change_log rows %d -> %d, want one more", len(before), len(evs))
	}
	last := evs[len(evs)-1]
	if last.Type != "plan_updated" || last.ProjectID != "mer" || last.SessionID != "" ||
		string(last.Payload) != `{"id":"plan_1"}` {
		t.Fatalf("last change = %+v payload=%s", last, last.Payload)
	}
}

func TestPlanDeleteCascades(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	seedPlanWithMilestones(t, s, at)
	if err := s.InsertPlanEvent(ctx, domain.PlanEvent{PlanID: "plan_1", Kind: "plan_created", CreatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlan(ctx, "plan_1"); err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	if _, ok, _ := s.GetPlan(ctx, "plan_1"); ok {
		t.Fatal("plan still present")
	}
	ms, err := s.ListMilestones(ctx, "plan_1")
	if err != nil || len(ms) != 0 {
		t.Fatalf("milestones after cascade = %+v err=%v", ms, err)
	}
	evs, err := s.ListPlanEvents(ctx, "plan_1", 0, 10)
	if err != nil || len(evs) != 0 {
		t.Fatalf("events after cascade = %+v err=%v", evs, err)
	}
}

func TestSessionPersistsPlanLinkAndSparsePaths(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec := sampleRecord("mer")
	rec.PlanID = "plan_1"
	rec.PlanMilestoneID = "ms_1"
	rec.SparsePaths = []string{"a", "c"}
	rec.PlanMilestoneKey = "m1"
	rec.PlanPrompt = "## Plan milestone"
	created, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := s.GetSession(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.PlanMilestoneKey != "m1" || got.PlanPrompt != "## Plan milestone" {
		t.Fatalf("plan key/prompt = %q %q", got.PlanMilestoneKey, got.PlanPrompt)
	}
	if got.PlanID != "plan_1" || got.PlanMilestoneID != "ms_1" || !reflect.DeepEqual(got.SparsePaths, []string{"a", "c"}) {
		t.Fatalf("plan link = %q %q %#v", got.PlanID, got.PlanMilestoneID, got.SparsePaths)
	}
	all, err := s.ListSessions(ctx, "mer")
	if err != nil || len(all) != 1 || all[0].PlanID != "plan_1" || len(all[0].SparsePaths) != 2 {
		t.Fatalf("list = %+v err=%v", all, err)
	}
	plain, _ := s.CreateSession(ctx, sampleRecord("mer"))
	got, _, _ = s.GetSession(ctx, plain.ID)
	if got.PlanID != "" || len(got.SparsePaths) != 0 {
		t.Fatalf("plain session plan link = %q %#v", got.PlanID, got.SparsePaths)
	}
}

func TestListPlanAttachedSessionIDsSkipsMilestoneWorkers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	worker := sampleRecord("mer")
	worker.PlanID = "plan_1"
	worker.PlanMilestoneID = "ms_1"
	worker.PlanMilestoneKey = "m1"
	if _, err := s.CreateSession(ctx, worker); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	var want []domain.SessionID
	for range 2 {
		rec := sampleRecord("mer")
		rec.PlanID = "plan_1"
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatalf("create research: %v", err)
		}
		want = append(want, created.ID)
	}
	other := sampleRecord("mer")
	other.PlanID = "plan_2"
	if _, err := s.CreateSession(ctx, other); err != nil {
		t.Fatalf("create other: %v", err)
	}
	if _, err := s.CreateSession(ctx, sampleRecord("mer")); err != nil {
		t.Fatalf("create plain: %v", err)
	}
	got, err := s.ListPlanAttachedSessionIDs(ctx, "plan_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attached = %v, want %v", got, want)
	}
}
