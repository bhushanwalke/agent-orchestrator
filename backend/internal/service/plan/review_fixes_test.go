package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func setSession(f fixture, id domain.SessionID, edit func(*domain.Session)) {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	s := f.sessions.sessions[id]
	edit(&s)
	f.sessions.sessions[id] = s
}

// C1: a human retry after three failed attempts starts a new worker (a new
// request id) and grants three more attempts.
func TestHumanRetryStartsANewWorkerWithAFreshBudget(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	for i := 0; i < 3; i++ {
		f.sessions.endSession(milestones(t, f, id)["m1"].SessionID)
		tick(t, f)
	}
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusFailed {
		t.Fatalf("m1 = %s", m.Status)
	}
	if _, err := f.svc.RetryMilestone(ctx, id, "m1"); err != nil {
		t.Fatal(err)
	}
	m := milestones(t, f, id)["m1"]
	if m.Status != domain.MilestoneStatusRunning || m.SessionID != "w-4" || f.sessions.spawnCount() != 4 {
		t.Fatalf("after retry m1 = %+v (spawns %d)", m, f.sessions.spawnCount())
	}
	if got := lastSpawn(f).ClientRequestID; !strings.HasSuffix(got, ":4") {
		t.Fatalf("retry reused a request id: %s", got)
	}
	for i := 0; i < 2; i++ {
		f.sessions.endSession(milestones(t, f, id)["m1"].SessionID)
		tick(t, f)
	}
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusRunning || f.sessions.spawnCount() != 6 {
		t.Fatalf("retry budget: m1 = %s spawns %d", m.Status, f.sessions.spawnCount())
	}
	if d, _ := f.svc.Get(ctx, id); d.Phase != domain.PlanPhaseImplementing {
		t.Fatalf("phase = %s", d.Phase)
	}
}

// C2: a QA session that ends after recording a defect does not start a new
// QA round before the fix, and the defect still reaches the live implementer.
func TestQAEndingAfterAFailWaitsForTheFix(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	m1 := milestones(t, f, id)["m1"]
	f.sessions.sendErrFor = map[domain.SessionID]error{m1.SessionID: apierr.Conflict("SESSION_COMPOSER_BUSY", "busy", nil)}
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "m1", "broken")); err != nil {
		t.Fatal(err)
	}
	f.sessions.endSession(qaSess)
	spawns := f.sessions.spawnCount()
	tick(t, f)
	tick(t, f)
	ms := milestones(t, f, id)
	if f.sessions.spawnCount() != spawns || ms["qa"].Status != domain.MilestoneStatusPending || ms["m1"].QAVerdict != domain.QAVerdictFail {
		t.Fatalf("spawns %d→%d qa=%s m1 verdict=%q", spawns, f.sessions.spawnCount(), ms["qa"].Status, ms["m1"].QAVerdict)
	}
	f.sessions.mu.Lock()
	f.sessions.sendErrFor = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	if got := milestones(t, f, id)["m1"]; got.Status != domain.MilestoneStatusRunning || got.SessionID != m1.SessionID {
		t.Fatalf("m1 = %+v", got)
	}
	reportDone(t, f, id, "m1", "fixed")
	if qa := milestones(t, f, id)["qa"]; qa.Status != domain.MilestoneStatusRunning || qa.SessionID == qaSess {
		t.Fatalf("new QA round = %+v", qa)
	}
}

// I3: a worker whose async start failed counts as ended.
func TestFailedProvisioningEndsTheMilestoneSession(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	m1 := milestones(t, f, id)["m1"]
	setSession(f, m1.SessionID, func(s *domain.Session) { s.ProvisionState = domain.SessionProvisionFailed })
	tick(t, f)
	if got := milestones(t, f, id)["m1"]; got.Attempt != 2 || got.SessionID == m1.SessionID {
		t.Fatalf("m1 = %+v", got)
	}
}

// I4: the branch a milestone worked on outlives its session row.
func TestMilestoneBranchSurvivesADeletedSession(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	m1 := milestones(t, f, id)["m1"]
	f.sessions.mu.Lock()
	delete(f.sessions.sessions, m1.SessionID)
	f.sessions.mu.Unlock()
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "m1", "broken")); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	if cfg := lastSpawn(f); cfg.PlanContext.MilestoneKey != "m1" || cfg.Branch != "ao/w-1/root" {
		t.Fatalf("respawn branch = %q", cfg.Branch)
	}
}

// I5: the "record a verdict" request is retried until the QA session takes it.
func TestQAVerdictRequestIsRetried(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	_, qaSess := toQA(t, f)
	f.sessions.sendErrFor = map[domain.SessionID]error{qaSess: apierr.Conflict("SESSION_COMPOSER_BUSY", "busy", nil)}
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: qaSess, State: domain.ReportDone})
	tick(t, f)
	if len(sentTo(f, qaSess)) != 0 {
		t.Fatal("refused message counted as sent")
	}
	f.sessions.mu.Lock()
	f.sessions.sendErrFor = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	tick(t, f)
	if msgs := sentTo(f, qaSess); len(msgs) != 1 || !strings.Contains(msgs[0], "ao plan qa-result") {
		t.Fatalf("verdict requests = %v", msgs)
	}
}

// I7: a finished, notified plan is no longer read on every tick.
func TestFinishedPlansAreNotTickedForever(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	if _, err := f.svc.RecordQAResult(ctx, id, planQAPass(qaSess)); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	if !strings.Contains(strings.Join(eventKinds(t, f, id), ","), "plan_done_notified") {
		t.Fatal("not notified")
	}
	f.sessions.mu.Lock()
	f.sessions.getCalls = 0
	f.sessions.mu.Unlock()
	before := len(eventKinds(t, f, id))
	tick(t, f)
	f.sessions.mu.Lock()
	gets := f.sessions.getCalls
	f.sessions.mu.Unlock()
	if gets != 0 || len(eventKinds(t, f, id)) != before {
		t.Fatalf("a finished plan is still read every tick: %d session reads, events %v", gets, eventKinds(t, f, id))
	}
}

// Minor (re-graded): a done report that lands while a defect is being
// delivered is not lost.
func TestDoneReportDuringDefectDeliveryIsKept(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	m1 := milestones(t, f, id)["m1"]
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "m1", "broken")); err != nil {
		t.Fatal(err)
	}
	f.sessions.sendHook = func(to domain.SessionID) {
		if to == m1.SessionID {
			f.svc.OnReport(ctx, domain.ReportRecord{SessionID: m1.SessionID, State: domain.ReportDone, Note: "fixed fast"})
		}
	}
	tick(t, f)
	if got := milestones(t, f, id)["m1"]; got.Status != domain.MilestoneStatusDone {
		t.Fatalf("m1 = %s", got.Status)
	}
}

// A blocked QA (a failure no milestone owns) stays for a person even if its
// session ends.
func TestBlockedQADoesNotRetryItself(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "", "main is broken")); err != nil {
		t.Fatal(err)
	}
	f.sessions.endSession(qaSess)
	spawns := f.sessions.spawnCount()
	tick(t, f)
	tick(t, f)
	if qa := milestones(t, f, id)["qa"]; qa.Status != domain.MilestoneStatusBlocked || f.sessions.spawnCount() != spawns {
		t.Fatalf("qa = %s spawns %d→%d", qa.Status, spawns, f.sessions.spawnCount())
	}
}

func TestSummaryCollectsMilestonePRs(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2"})
	ms := milestones(t, f, id)
	setSession(f, ms["m1"].SessionID, func(s *domain.Session) { s.PRs = []domain.PRFacts{{URL: "https://x/pull/1", Number: 1}} })
	setSession(f, ms["m2"].SessionID, func(s *domain.Session) {
		s.PRs = []domain.PRFacts{{URL: "https://x/pull/2", Number: 2}, {URL: "https://x/pull/1", Number: 1}}
		s.IsTerminated = true
	})
	list, err := f.svc.List(context.Background(), "mer")
	if err != nil || len(list) != 1 || len(list[0].PRs) != 2 || list[0].PRs[0].Number != 1 || list[0].PRs[1].Number != 2 {
		t.Fatalf("plan PRs = %+v err=%v", list, err)
	}
}

// Re-running QA from the run view: a passed (or failed/blocked) qa milestone
// can be retried once every implement milestone is done; a pending one cannot.
func TestRerunQAAfterItPassed(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	if _, err := f.svc.RecordQAResult(ctx, id, planQAPass(qaSess)); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	if _, err := f.svc.RetryMilestone(ctx, id, "qa"); err != nil {
		t.Fatalf("re-run QA: %v", err)
	}
	qa := milestones(t, f, id)["qa"]
	if qa.Status != domain.MilestoneStatusRunning || qa.SessionID == qaSess || qa.QAVerdict != domain.QAVerdictNone {
		t.Fatalf("re-run qa = %+v", qa)
	}
	f2 := flowFixture(t, domain.ProjectConfig{})
	id2 := activePlan(t, f2, 3, msSpec{key: "m1"})
	_, err := f2.svc.RetryMilestone(ctx, id2, "qa")
	wantCode(t, err, "MILESTONE_NOT_RETRYABLE")
}
