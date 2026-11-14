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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

type sentMessage struct {
	ID   domain.SessionID
	Text string
	Opts ports.MessageDeliveryOptions
}

type fakeSessions struct {
	mu          sync.Mutex
	ensureCalls int
	ensureErr   error
	sendErr     error
	sent        []sentMessage
	sessions    map[domain.SessionID]domain.Session
	// spawns records every SpawnWorker config; spawnErr fails the next spawns.
	spawns   []ports.SpawnConfig
	spawnErr error
	// spawnHook runs inside SpawnWorker before the session exists.
	spawnHook func(ports.SpawnConfig)
	// sendErrFor refuses delivery to one session.
	sendErrFor map[domain.SessionID]error
	nextID     int
	byRequest  map[string]domain.SessionID
	getCalls   int
	// sendHook runs at the start of Send, outside the fake's lock.
	sendHook func(domain.SessionID)
}

func (f *fakeSessions) EnsureOrchestrator(_ context.Context, projectID domain.ProjectID) (domain.SessionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls++
	if f.ensureErr != nil {
		return domain.SessionRecord{}, f.ensureErr
	}
	rec := domain.SessionRecord{ID: "orch-1", ProjectID: projectID, Kind: domain.KindOrchestrator}
	if f.sessions != nil {
		if _, ok := f.sessions[rec.ID]; !ok {
			f.sessions[rec.ID] = domain.Session{SessionRecord: rec}
		}
	}
	return rec, nil
}

func (f *fakeSessions) SpawnWorker(_ context.Context, cfg ports.SpawnConfig, _ domain.ProjectRecord) (domain.SessionRecord, error) {
	f.mu.Lock()
	hook := f.spawnHook
	f.mu.Unlock()
	if hook != nil {
		hook(cfg)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// Like the session manager, a committed request id replays its session.
	if prior, ok := f.byRequest[cfg.ClientRequestID]; ok && cfg.ClientRequestID != "" {
		return f.sessions[prior].SessionRecord, nil
	}
	f.spawns = append(f.spawns, cfg)
	if f.spawnErr != nil {
		return domain.SessionRecord{}, f.spawnErr
	}
	f.nextID++
	id := domain.SessionID(fmt.Sprintf("w-%d", f.nextID))
	branch := cfg.Branch
	if branch == "" {
		branch = "ao/" + string(id) + "/root"
	}
	rec := domain.SessionRecord{ID: id, ProjectID: cfg.ProjectID, Kind: domain.KindWorker, DisplayName: cfg.DisplayName,
		Metadata: domain.SessionMetadata{Branch: branch, WorkspacePath: "/wt/" + string(id)}}
	f.sessions[id] = domain.Session{SessionRecord: rec}
	if f.byRequest == nil {
		f.byRequest = map[string]domain.SessionID{}
	}
	f.byRequest[cfg.ClientRequestID] = id
	return rec, nil
}

// endSession marks a session terminated, as a kill would.
func (f *fakeSessions) endSession(id domain.SessionID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.sessions[id]
	s.IsTerminated = true
	f.sessions[id] = s
}

func (f *fakeSessions) spawnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.spawns)
}

func (f *fakeSessions) Send(_ context.Context, id domain.SessionID, text string, opts ports.MessageDeliveryOptions) error {
	f.mu.Lock()
	hook := f.sendHook
	f.sendHook = nil
	f.mu.Unlock()
	if hook != nil {
		hook(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	if err := f.sendErrFor[id]; err != nil {
		return err
	}
	f.sent = append(f.sent, sentMessage{ID: id, Text: text, Opts: opts})
	return nil
}

func (f *fakeSessions) Get(_ context.Context, id domain.SessionID) (domain.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls++
	s, ok := f.sessions[id]
	return s, ok, nil
}

type fakeScheduler struct{ ticks int }

func (f *fakeScheduler) Tick(context.Context, time.Time) error { f.ticks++; return nil }

type fixture struct {
	svc        *plan.Service
	store      *sqlite.Store
	sessions   *fakeSessions
	sched      *fakeScheduler
	workspaces *fakeWorkspaces
}

func newFixture(t *testing.T, kind domain.ProjectKind) fixture {
	t.Helper()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(context.Background(), domain.ProjectRecord{
		ID: "mer", Path: "/tmp/mer", DisplayName: "Mercury", Kind: kind,
		RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{sessions: map[domain.SessionID]domain.Session{}}
	sched := &fakeScheduler{}
	n := 0
	svc := plan.New(plan.Deps{
		Store: st, Sessions: sessions, Projects: st, Scheduler: sched, KickoffRetryInterval: -1,
		Now:   func() time.Time { return time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC) },
		NewID: func(prefix string) string { n++; return fmt.Sprintf("%s%d", prefix, n) },
	})
	return fixture{svc: svc, store: st, sessions: sessions, sched: sched}
}

func wantCode(t *testing.T, err error, code string) *apierr.Error {
	t.Helper()
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return ae
}

func eventKinds(t *testing.T, f fixture, id domain.PlanID) []string {
	t.Helper()
	evs, err := f.store.ListPlanEvents(context.Background(), id, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func createPlan(t *testing.T, f fixture) domain.Plan {
	t.Helper()
	p, err := f.svc.Create(context.Background(), plan.CreateInput{
		ProjectID: "mer", Title: "Demo", Brief: "Add X to the API", TicketRef: "github:o/r#12",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return p
}

func addMs(t *testing.T, f fixture, id domain.PlanID, key string, deps ...string) {
	t.Helper()
	if _, err := f.svc.AddMilestone(context.Background(), id, plan.MilestoneInput{
		Key: key, Title: "Do " + key, Description: "Implement " + key, DependsOn: deps,
	}); err != nil {
		t.Fatalf("add %s: %v", key, err)
	}
}

func TestCreateSendsKickoffToEnsuredOrchestrator(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	p := createPlan(t, f)

	if p.State != domain.PlanStatePlanning || p.OrchestratorSessionID != "orch-1" || p.MaxParallel != 3 ||
		!strings.HasPrefix(string(p.ID), "plan_") {
		t.Fatalf("plan = %+v", p)
	}
	stored, ok, _ := f.store.GetPlan(context.Background(), p.ID)
	if !ok || stored.State != domain.PlanStatePlanning || stored.OrchestratorSessionID != "orch-1" {
		t.Fatalf("stored plan = %+v ok=%v", stored, ok)
	}
	if len(f.sessions.sent) != 1 {
		t.Fatalf("sent = %+v", f.sessions.sent)
	}
	msg := f.sessions.sent[0]
	if msg.ID != "orch-1" || msg.Opts.SenderSessionID != "" || msg.Opts.AuthoredByUser {
		t.Fatalf("message target/opts = %+v", msg)
	}
	for _, want := range []string{
		fmt.Sprintf(`<ao-plan-request id="%s">`, p.ID), "project Mercury", "Title: Demo", "Ticket: github:o/r#12",
		"Add X to the API", fmt.Sprintf("ao plan set-spec %s --file", p.ID), fmt.Sprintf("ao plan activate %s", p.ID),
		"</ao-plan-request>",
	} {
		if !strings.Contains(msg.Text, want) {
			t.Fatalf("kickoff missing %q:\n%s", want, msg.Text)
		}
	}
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,kickoff_sent" {
		t.Fatalf("events = %s", got)
	}
}

func TestCreateDefersKickoffWhenDeliveryRefused(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	f.sessions.sendErr = apierr.Conflict("SESSION_BUSY", "busy", nil)
	p := createPlan(t, f)
	if p.State != domain.PlanStatePlanning || p.OrchestratorSessionID != "orch-1" {
		t.Fatalf("plan = %+v", p)
	}
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,kickoff_deferred" {
		t.Fatalf("events = %s", got)
	}
}

func TestDeferredKickoffIsRetriedUntilDelivered(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	f.sessions.sendErr = apierr.Conflict("SESSION_STARTUP_PENDING", "starting", nil)
	p := createPlan(t, f)
	done, err := f.svc.RetryDeferredKickoff(context.Background(), p.ID)
	if err != nil || done {
		t.Fatalf("retry while refused: done=%v err=%v", done, err)
	}
	f.sessions.mu.Lock()
	f.sessions.sendErr = nil
	f.sessions.mu.Unlock()
	if done, err := f.svc.RetryDeferredKickoff(context.Background(), p.ID); err != nil || !done {
		t.Fatalf("retry after startup: done=%v err=%v", done, err)
	}
	if done, err := f.svc.RetryDeferredKickoff(context.Background(), p.ID); err != nil || !done {
		t.Fatalf("retry after delivered: done=%v err=%v", done, err)
	}
	if len(f.sessions.sent) != 1 {
		t.Fatalf("kickoff delivered %d times", len(f.sessions.sent))
	}
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,kickoff_deferred,kickoff_sent" {
		t.Fatalf("events = %s", got)
	}
}

func TestDeferredKickoffBackgroundRetry(t *testing.T) {
	st := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	sessions := &fakeSessions{sendErr: errors.New("starting")}
	svc := plan.New(plan.Deps{Store: st, Sessions: sessions, Projects: st, KickoffRetryInterval: 10 * time.Millisecond, KickoffRetryTimeout: 5 * time.Second})
	p, err := svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	sessions.mu.Lock()
	sessions.sendErr = nil
	sessions.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sessions.mu.Lock()
		n := len(sessions.sent)
		sessions.mu.Unlock()
		if n == 1 {
			evs, _ := st.ListPlanEvents(ctx, p.ID, 0, 10)
			if evs[len(evs)-1].Kind == "kickoff_sent" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deferred kickoff was not retried in the background")
}

func TestCreateDefersKickoffWhenOrchestratorCannotStart(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	f.sessions.ensureErr = errors.New("harness not installed")
	p := createPlan(t, f)
	if p.OrchestratorSessionID != "" {
		t.Fatalf("plan = %+v", p)
	}
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,kickoff_deferred" {
		t.Fatalf("events = %s", got)
	}
}

func TestCreateValidation(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	cases := []struct {
		in   plan.CreateInput
		code string
	}{
		{plan.CreateInput{ProjectID: "mer", Title: " ", Brief: "b"}, "INVALID_PLAN"},
		{plan.CreateInput{ProjectID: "mer", Title: strings.Repeat("t", 101), Brief: "b"}, "INVALID_PLAN"},
		{plan.CreateInput{ProjectID: "mer", Title: "t", Brief: ""}, "INVALID_PLAN"},
		{plan.CreateInput{ProjectID: "mer", Title: "t", Brief: strings.Repeat("b", domain.PlanBriefMaxBytes+1)}, "INVALID_PLAN"},
		{plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b", MaxParallel: intPtr(11)}, "INVALID_PLAN"},
		{plan.CreateInput{ProjectID: "nope", Title: "t", Brief: "b"}, "PROJECT_NOT_FOUND"},
	}
	for _, tc := range cases {
		_, err := f.svc.Create(ctx, tc.in)
		wantCode(t, err, tc.code)
	}
}

func TestCreateRefusesScratchAndWorkspaceProjects(t *testing.T) {
	for _, kind := range []domain.ProjectKind{domain.ProjectKindScratch, domain.ProjectKindWorkspace} {
		f := newFixture(t, kind)
		_, err := f.svc.Create(context.Background(), plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b"})
		wantCode(t, err, "PLAN_PROJECT_KIND_UNSUPPORTED")
		if f.sessions.ensureCalls != 0 {
			t.Fatalf("orchestrator ensured for %s project", kind)
		}
	}
}

func TestMilestoneEditing(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2", "m1")

	_, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "m1", Title: "x", Description: "y"})
	wantCode(t, err, "MILESTONE_KEY_EXISTS")
	_, err = f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "Bad", Title: "x", Description: "y"})
	if ae := wantCode(t, err, "INVALID_MILESTONE"); ae.Details["field"] != "key" {
		t.Fatalf("details = %+v", ae.Details)
	}
	_, err = f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "m3", Title: "x", Description: "y", DependsOn: []string{"zz"}})
	if ae := wantCode(t, err, "INVALID_MILESTONE"); ae.Details["field"] != "dependsOn" {
		t.Fatalf("details = %+v", ae.Details)
	}
	_, err = f.svc.AddMilestone(ctx, "plan_missing", plan.MilestoneInput{Key: "m3", Title: "x", Description: "y"})
	wantCode(t, err, "PLAN_NOT_FOUND")

	title := "Renamed"
	deps := []string{}
	ms, err := f.svc.UpdateMilestone(ctx, p.ID, "m2", plan.MilestonePatch{Title: &title, DependsOn: &deps})
	if err != nil || ms.Title != "Renamed" || len(ms.DependsOn) != 0 {
		t.Fatalf("update = %+v err=%v", ms, err)
	}
	_, err = f.svc.UpdateMilestone(ctx, p.ID, "zz", plan.MilestonePatch{Title: &title})
	wantCode(t, err, "MILESTONE_NOT_FOUND")

	if err := f.svc.RemoveMilestone(ctx, p.ID, "m2"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	wantCode(t, f.svc.RemoveMilestone(ctx, p.ID, "m2"), "MILESTONE_NOT_FOUND")

	spec, err := f.svc.SetSpec(ctx, p.ID, "# Plan\n")
	if err != nil || spec.Plan.SpecMarkdown != "# Plan\n" {
		t.Fatalf("set spec = %+v err=%v", spec, err)
	}
	kinds := strings.Join(eventKinds(t, f, p.ID), ",")
	if !strings.Contains(kinds, "milestone_added,milestone_added,milestone_updated,milestone_removed,spec_set") {
		t.Fatalf("events = %s", kinds)
	}
}

func TestRemoveMilestoneWithDependentsIsRefused(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2", "m1")
	wantCode(t, f.svc.RemoveMilestone(context.Background(), p.ID, "m1"), "MILESTONE_HAS_DEPENDENTS")
}

func TestTooManyMilestones(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	p := createPlan(t, f)
	for i := 0; i < domain.MaxPlanMilestones-1; i++ {
		addMs(t, f, p.ID, fmt.Sprintf("m%d", i))
	}
	if _, err := f.svc.AddMilestone(context.Background(), p.ID, plan.MilestoneInput{Key: "qa", Kind: domain.MilestoneKindQA, Title: "QA"}); err != nil {
		t.Fatalf("qa fills the last slot: %v", err)
	}
	_, err := f.svc.AddMilestone(context.Background(), p.ID, plan.MilestoneInput{Key: "extra", Title: "x", Description: "y"})
	wantCode(t, err, "PLAN_TOO_MANY_MILESTONES")
}

func TestActivateWithoutImplementMilestoneIsInvalid(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	p := createPlan(t, f)
	_, err := f.svc.Activate(context.Background(), p.ID)
	wantCode(t, err, "PLAN_GRAPH_INVALID")
	if f.sched.ticks != 0 {
		t.Fatal("scheduler ticked for an invalid plan")
	}
}

func TestActivateRejectsCycle(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2", "m1")
	deps := []string{"m2"}
	if _, err := f.svc.UpdateMilestone(ctx, p.ID, "m1", plan.MilestonePatch{DependsOn: &deps}); err != nil {
		t.Fatalf("cycle edit should be accepted while planning: %v", err)
	}
	_, err := f.svc.Activate(ctx, p.ID)
	ae := wantCode(t, err, "PLAN_GRAPH_INVALID")
	if !strings.Contains(ae.Message, "m1") || !strings.Contains(ae.Message, "m2") {
		t.Fatalf("message = %q", ae.Message)
	}
	got, _ := f.svc.Get(ctx, p.ID)
	if got.Plan.State != domain.PlanStatePlanning {
		t.Fatalf("state = %s", got.Plan.State)
	}
}

func TestActivateAddsQAAndLocksEditing(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2", "m1")

	d, err := f.svc.Activate(ctx, p.ID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if d.Plan.State != domain.PlanStateActive || d.Plan.ActivatedAt == nil || d.Phase != domain.PlanPhaseImplementing {
		t.Fatalf("detail = %+v", d.Summary)
	}
	if len(d.Milestones) != 3 {
		t.Fatalf("milestones = %+v", d.Milestones)
	}
	qa := d.Milestones[2].Milestone
	if qa.Key != "qa" || qa.Kind != domain.MilestoneKindQA || qa.Title != "QA: verify the whole feature" ||
		qa.Status != domain.MilestoneStatusPending {
		t.Fatalf("qa = %+v", qa)
	}
	if string(d.DisplayStatus) != "Implementing 0/2" {
		t.Fatalf("display = %q", d.DisplayStatus)
	}
	if f.sched.ticks != 1 {
		t.Fatalf("ticks = %d", f.sched.ticks)
	}

	_, err = f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "m3", Title: "x", Description: "y"})
	wantCode(t, err, "PLAN_NOT_EDITABLE")
	title := "t"
	_, err = f.svc.UpdateMilestone(ctx, p.ID, "m1", plan.MilestonePatch{Title: &title})
	wantCode(t, err, "PLAN_NOT_EDITABLE")
	wantCode(t, f.svc.RemoveMilestone(ctx, p.ID, "m1"), "PLAN_NOT_EDITABLE")
	_, err = f.svc.SetSpec(ctx, p.ID, "x")
	wantCode(t, err, "PLAN_NOT_EDITABLE")
	_, err = f.svc.Activate(ctx, p.ID)
	wantCode(t, err, "PLAN_NOT_EDITABLE")
	_, err = f.svc.Update(ctx, p.ID, plan.Patch{Title: &title})
	wantCode(t, err, "PLAN_NOT_EDITABLE")
	mp := 5
	updated, err := f.svc.Update(ctx, p.ID, plan.Patch{MaxParallel: &mp})
	if err != nil || updated.Plan.MaxParallel != 5 {
		t.Fatalf("max parallel stays editable: %+v err=%v", updated, err)
	}
}

func TestActivateKeepsExplicitQAMilestone(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	if _, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "qa", Kind: domain.MilestoneKindQA, Title: "Custom QA"}); err != nil {
		t.Fatalf("add qa: %v", err)
	}
	d, err := f.svc.Activate(ctx, p.ID)
	if err != nil || len(d.Milestones) != 2 || d.Milestones[1].Milestone.Title != "Custom QA" {
		t.Fatalf("detail = %+v err=%v", d.Milestones, err)
	}
}

func TestCancelMarksOpenMilestonesCancelled(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMilestones(ctx, p.ID)
	ms[0].Status = domain.MilestoneStatusDone
	if err := f.store.UpdateMilestone(ctx, ms[0]); err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.svc.Cancel(ctx, p.ID)
	if err != nil || cancelled.Plan.State != domain.PlanStateCancelled {
		t.Fatalf("cancel = %+v err=%v", cancelled, err)
	}
	d, _ := f.svc.Get(ctx, p.ID)
	statuses := []domain.MilestoneStatus{}
	for _, m := range d.Milestones {
		statuses = append(statuses, m.Milestone.Status)
	}
	want := []domain.MilestoneStatus{domain.MilestoneStatusDone, domain.MilestoneStatusCancelled, domain.MilestoneStatusCancelled}
	if fmt.Sprint(statuses) != fmt.Sprint(want) || d.Phase != domain.PlanPhaseCancelled {
		t.Fatalf("statuses = %v phase = %s", statuses, d.Phase)
	}
	if kinds := eventKinds(t, f, p.ID); kinds[len(kinds)-1] != "plan_cancelled" {
		t.Fatalf("events = %v", kinds)
	}
	_, err = f.svc.Cancel(ctx, "plan_missing")
	wantCode(t, err, "PLAN_NOT_FOUND")
}

func TestRetryMilestoneResetsFailed(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.RetryMilestone(ctx, p.ID, "m1")
	wantCode(t, err, "MILESTONE_NOT_RETRYABLE")

	ms, _ := f.store.ListMilestones(ctx, p.ID)
	ms[0].Status = domain.MilestoneStatusFailed
	ms[0].Attempt = 3
	if err := f.store.UpdateMilestone(ctx, ms[0]); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.Get(ctx, p.ID)
	if err != nil || d.Phase != domain.PlanPhaseFailed {
		t.Fatalf("phase before retry = %s err=%v", d.Phase, err)
	}
	ticks := f.sched.ticks
	d, err = f.svc.RetryMilestone(ctx, p.ID, "m1")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	m1 := d.Milestones[0].Milestone
	if m1.Status != domain.MilestoneStatusReady || m1.AttemptsUsed() != 0 || m1.Attempt != 3 || d.Phase != domain.PlanPhaseImplementing {
		t.Fatalf("after retry m1 = %+v phase = %s", m1, d.Phase)
	}
	if f.sched.ticks != ticks+1 {
		t.Fatalf("retry did not tick the scheduler")
	}
}

func TestQAResultAuthorizationAndVerdicts(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMilestones(ctx, p.ID)
	for i := range ms {
		switch ms[i].Key {
		case "qa":
			ms[i].Status = domain.MilestoneStatusRunning
			ms[i].SessionID = "qa-sess"
			ms[i].Attempt = 1
		default:
			ms[i].Status = domain.MilestoneStatusDone
			ms[i].SessionID = domain.SessionID("w-" + ms[i].Key)
		}
		if err := f.store.UpdateMilestone(ctx, ms[i]); err != nil {
			t.Fatal(err)
		}
	}

	_, err := f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictPass, SessionID: "w-m1"})
	wantCode(t, err, "PLAN_QA_SESSION_REQUIRED")
	_, err = f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictPass})
	wantCode(t, err, "PLAN_QA_SESSION_REQUIRED")
	_, err = f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictFail, SessionID: "qa-sess", MilestoneKey: "zz", Note: "x"})
	wantCode(t, err, "MILESTONE_NOT_FOUND")
	_, err = f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: "maybe", SessionID: "qa-sess"})
	wantCode(t, err, "INVALID_QA_RESULT")

	d, err := f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{
		Verdict: domain.QAVerdictFail, MilestoneKey: "m2", Note: "button broken", SessionID: "qa-sess",
	})
	if err != nil {
		t.Fatalf("fail verdict: %v", err)
	}
	byKey := map[string]domain.Milestone{}
	for _, v := range d.Milestones {
		byKey[v.Milestone.Key] = v.Milestone
	}
	if m2 := byKey["m2"]; m2.Status != domain.MilestoneStatusReady || m2.QAVerdict != domain.QAVerdictFail || m2.QANote != "button broken" {
		t.Fatalf("m2 = %+v", m2)
	}
	// QA keeps running to record other findings; its done report sends it
	// back to pending.
	if qa := byKey["qa"]; qa.Status != domain.MilestoneStatusRunning || qa.QAVerdict != domain.QAVerdictFail {
		t.Fatalf("qa = %+v", qa)
	}
	if d.Phase != domain.PlanPhaseFixing {
		t.Fatalf("phase = %s", d.Phase)
	}
}

func TestQAPassMarksPlanDone(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMilestones(ctx, p.ID)
	ms[0].Status = domain.MilestoneStatusDone
	ms[1].Status = domain.MilestoneStatusRunning
	ms[1].SessionID = "qa-sess"
	for _, m := range ms {
		if err := f.store.UpdateMilestone(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	d, err := f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictPass, Note: "all good", SessionID: "qa-sess"})
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if d.Phase != domain.PlanPhaseDone || d.Milestones[1].Milestone.QAVerdict != domain.QAVerdictPass {
		t.Fatalf("phase = %s qa = %+v", d.Phase, d.Milestones[1].Milestone)
	}
	if kinds := eventKinds(t, f, p.ID); kinds[len(kinds)-1] != "qa_passed" {
		t.Fatalf("events = %v", kinds)
	}
}

func TestListAndEvents(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	f.sessions.sessions["orch-1"] = domain.Session{SessionRecord: domain.SessionRecord{ID: "orch-1"}}

	list, err := f.svc.List(ctx, "mer")
	if err != nil || len(list) != 1 || list[0].Plan.ID != p.ID || list[0].Phase != domain.PlanPhasePlanning ||
		list[0].Counts.Total != 1 {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	page, err := f.svc.Events(ctx, p.ID, 0, 2)
	evs := page.Events
	if err != nil || len(evs) != 2 || evs[0].Kind != "plan_created" || page.NextAfter != evs[1].ID {
		t.Fatalf("events = %+v err=%v", page, err)
	}
	more, err := f.svc.Events(ctx, p.ID, page.NextAfter, 50)
	if err != nil || len(more.Events) != 1 || more.Events[0].Kind != "milestone_added" || more.NextAfter != more.Events[0].ID ||
		more.MilestoneKeys[more.Events[0].MilestoneID] != "m1" {
		t.Fatalf("more = %+v err=%v", more, err)
	}
	_, err = f.svc.Events(ctx, "plan_missing", 0, 10)
	wantCode(t, err, "PLAN_NOT_FOUND")
}

func TestQAResultRequiresRunningQA(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMilestones(ctx, p.ID)
	// QA went back to pending after a fail but still names its old session.
	ms[0].Status = domain.MilestoneStatusRunning
	ms[0].QAVerdict = domain.QAVerdictFail
	ms[1].Status = domain.MilestoneStatusPending
	ms[1].SessionID = "old-qa"
	for _, m := range ms {
		if err := f.store.UpdateMilestone(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_, err := f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictPass, Note: "ok", SessionID: "old-qa"})
	wantCode(t, err, "PLAN_QA_NOT_RUNNING")
	_, err = f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictFail, MilestoneKey: "m1", Note: "x", SessionID: "old-qa"})
	wantCode(t, err, "PLAN_QA_NOT_RUNNING")
	if _, err := f.svc.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.RecordQAResult(ctx, p.ID, plan.QAResultInput{Verdict: domain.QAVerdictPass, Note: "ok", SessionID: "old-qa"})
	wantCode(t, err, "PLAN_NOT_ACTIVE")
}

func TestUpdateAndCancelReturnDerivedSummary(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "m1")
	addMs(t, f, p.ID, "m2")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	ms, _ := f.store.ListMilestones(ctx, p.ID)
	ms[0].Status = domain.MilestoneStatusDone
	if err := f.store.UpdateMilestone(ctx, ms[0]); err != nil {
		t.Fatal(err)
	}
	mp := 4
	sum, err := f.svc.Update(ctx, p.ID, plan.Patch{MaxParallel: &mp})
	if err != nil || sum.Plan.MaxParallel != 4 || sum.Counts.Total != 3 || string(sum.DisplayStatus) != "Implementing 1/2" {
		t.Fatalf("update summary = %+v err=%v", sum, err)
	}
	sum, err = f.svc.Cancel(ctx, p.ID)
	if err != nil || sum.Phase != domain.PlanPhaseCancelled || sum.Counts.Done != 1 || sum.Counts.Total != 3 {
		t.Fatalf("cancel summary = %+v err=%v", sum, err)
	}
}

type countingSessions struct {
	*fakeSessions
	inSend chan struct{}
	gate   chan struct{}
}

func (c *countingSessions) Send(ctx context.Context, id domain.SessionID, text string, opts ports.MessageDeliveryOptions) error {
	c.mu.Lock()
	refuse := c.sendErr != nil
	c.mu.Unlock()
	if !refuse {
		select {
		case c.inSend <- struct{}{}:
		default:
		}
		<-c.gate
	}
	return c.fakeSessions.Send(ctx, id, text, opts)
}

func TestConcurrentKickoffRetriesDeliverOnce(t *testing.T) {
	st := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	sessions := &countingSessions{fakeSessions: &fakeSessions{sendErr: errors.New("starting")}, inSend: make(chan struct{}, 1), gate: make(chan struct{})}
	svc := plan.New(plan.Deps{Store: st, Sessions: sessions, Projects: st, KickoffRetryInterval: -1})
	p, err := svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	sessions.mu.Lock()
	sessions.sendErr = nil
	sessions.mu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.RetryDeferredKickoff(ctx, p.ID)
		}()
	}
	<-sessions.inSend
	time.Sleep(50 * time.Millisecond) // let the second retry reach its check
	close(sessions.gate)
	wg.Wait()
	if n := len(sessions.sent); n != 1 {
		t.Fatalf("kickoff delivered %d times", n)
	}
}

func TestMilestoneCapLeavesRoomForQA(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	for i := 0; i < domain.MaxPlanMilestones-1; i++ {
		addMs(t, f, p.ID, fmt.Sprintf("m%d", i))
	}
	_, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "extra", Title: "x", Description: "y"})
	wantCode(t, err, "PLAN_TOO_MANY_MILESTONES")
	if _, err := f.svc.Activate(ctx, p.ID); err != nil {
		t.Fatalf("activate with %d implement milestones: %v", domain.MaxPlanMilestones-1, err)
	}
}

func TestQADependsOnIsRejectedAndDerivedOnRead(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	addMs(t, f, p.ID, "a1")
	addMs(t, f, p.ID, "b1")
	_, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "qa", Kind: domain.MilestoneKindQA, Title: "QA", DependsOn: []string{"a1"}})
	if ae := wantCode(t, err, "INVALID_MILESTONE"); ae.Details["field"] != "dependsOn" {
		t.Fatalf("details = %+v", ae.Details)
	}
	qa, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "qa", Kind: domain.MilestoneKindQA, Title: "QA"})
	if err != nil || fmt.Sprint(qa.DependsOn) != "[a1 b1]" {
		t.Fatalf("added qa = %+v err=%v", qa, err)
	}
	deps := []string{"b1"}
	_, err = f.svc.UpdateMilestone(ctx, p.ID, "qa", plan.MilestonePatch{DependsOn: &deps})
	if ae := wantCode(t, err, "INVALID_MILESTONE"); ae.Details["field"] != "dependsOn" {
		t.Fatalf("details = %+v", ae.Details)
	}
	addMs(t, f, p.ID, "c1")
	d, err := f.svc.Activate(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Milestones[2].Milestone; got.Key != "qa" || fmt.Sprint(got.DependsOn) != "[a1 b1 c1]" {
		t.Fatalf("qa deps on read = %+v", got)
	}
	stored, _ := f.store.ListMilestones(ctx, p.ID)
	for _, m := range stored {
		if m.Kind == domain.MilestoneKindQA && len(m.DependsOn) != 0 {
			t.Fatalf("qa deps were stored: %v", m.DependsOn)
		}
	}
	list, _ := f.svc.List(ctx, "mer")
	if len(list) != 1 || list[0].Counts.Total != 4 {
		t.Fatalf("list = %+v", list)
	}
}

func TestCreateMaxParallelExplicitZeroIsInvalid(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	for _, n := range []int{0, 11} {
		v := n
		_, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b", MaxParallel: &v})
		if ae := wantCode(t, err, "INVALID_PLAN"); ae.Details["field"] != "maxParallel" {
			t.Fatalf("%d: details = %+v", n, ae.Details)
		}
	}
	p, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b"})
	if err != nil || p.MaxParallel != 3 {
		t.Fatalf("nil max parallel = %+v err=%v", p, err)
	}
}

func TestCancelSetsFinishedAtAndEventNotesCarryTitles(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	ctx := context.Background()
	p := createPlan(t, f)
	if _, err := f.svc.AddMilestone(ctx, p.ID, plan.MilestoneInput{Key: "m1", Title: "Build the API", Description: "x"}); err != nil {
		t.Fatal(err)
	}
	title := "Build the REST API"
	if _, err := f.svc.UpdateMilestone(ctx, p.ID, "m1", plan.MilestonePatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveMilestone(ctx, p.ID, "m1"); err != nil {
		t.Fatal(err)
	}
	evs, _ := f.store.ListPlanEvents(ctx, p.ID, 0, 100)
	notes := map[string]string{}
	for _, e := range evs {
		notes[e.Kind] = e.Note
	}
	if notes["milestone_added"] != "Build the API" || notes["milestone_updated"] != "Build the REST API" ||
		notes["milestone_removed"] != "m1: Build the REST API" {
		t.Fatalf("notes = %+v", notes)
	}
	sum, err := f.svc.Cancel(ctx, p.ID)
	if err != nil || sum.Plan.FinishedAt == nil {
		t.Fatalf("cancelled plan finishedAt = %v err=%v", sum.Plan.FinishedAt, err)
	}
}

func intPtr(n int) *int { return &n }

// fakeWorkspaces answers the worktree branch probe. By default a worktree is
// on its session's recorded branch; checkedOut overrides a path and failFor
// makes the probe fail.
type fakeWorkspaces struct {
	mu         sync.Mutex
	sessions   *fakeSessions
	checkedOut map[string]string
	failFor    map[string]bool
}

func (f *fakeWorkspaces) CurrentBranch(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failFor[path] {
		return "", errors.New("not a git repository")
	}
	if b, ok := f.checkedOut[path]; ok {
		return b, nil
	}
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	for _, sess := range f.sessions.sessions {
		if sess.Metadata.WorkspacePath == path {
			return sess.Metadata.Branch, nil
		}
	}
	return "", errors.New("no worktree")
}
