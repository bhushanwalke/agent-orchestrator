package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// toQA runs m1 and m2 to done and returns the QA session.
func toQA(t *testing.T, f fixture) (domain.PlanID, domain.SessionID) {
	t.Helper()
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2"})
	reportDone(t, f, id, "m1", "built m1")
	reportDone(t, f, id, "m2", "built m2")
	qa := milestones(t, f, id)["qa"]
	if qa.Status != domain.MilestoneStatusRunning || qa.SessionID == "" {
		t.Fatalf("qa = %+v", qa)
	}
	return id, qa.SessionID
}

func sentTo(f fixture, id domain.SessionID) []string {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	var out []string
	for _, m := range f.sessions.sent {
		if m.ID == id {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestQAPassFinishesThePlanAndTellsTheOrchestrator(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	f.sessions.sendErrFor = map[domain.SessionID]error{"orch-1": apierr.Conflict("SESSION_COMPOSER_BUSY", "busy", nil)}
	if _, err := f.svc.RecordQAResult(ctx, id, planQAPass(qaSess)); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	d, _ := f.svc.Get(ctx, id)
	if d.Phase != domain.PlanPhaseDone || d.Plan.FinishedAt == nil {
		t.Fatalf("phase = %s finished=%v", d.Phase, d.Plan.FinishedAt)
	}
	kinds := strings.Join(eventKinds(t, f, id), ",")
	if !strings.Contains(kinds, "qa_passed") || strings.Count(kinds, "plan_done") != 1 || strings.Contains(kinds, "plan_done_notified") {
		t.Fatalf("events while the orchestrator is busy = %s", kinds)
	}
	f.sessions.mu.Lock()
	f.sessions.sendErrFor = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	tick(t, f)
	msgs := sentTo(f, "orch-1")
	done := msgs[len(msgs)-1]
	if !strings.HasPrefix(done, `<ao-plan-done id="`+string(id)+`">`) || !strings.Contains(done, "m1: Title m1 — session w-1, branch ao/w-1/root") ||
		!strings.Contains(done, "QA verified: verified") {
		t.Fatalf("done message:\n%s", done)
	}
	kinds = strings.Join(eventKinds(t, f, id), ",")
	if strings.Count(kinds, "plan_done_notified") != 1 || strings.Count(kinds, "plan_done,") != 1 {
		t.Fatalf("events = %s", kinds)
	}
}

// A QA failure goes back to the live implementer through the guarded send; no
// second worker is started.
func TestQAFailReusesTheLiveImplementer(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	m1 := milestones(t, f, id)["m1"]
	spawns := f.sessions.spawnCount()
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "m1", "POST /todos returns 500")); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	got := milestones(t, f, id)["m1"]
	if got.Status != domain.MilestoneStatusRunning || got.SessionID != m1.SessionID || f.sessions.spawnCount() != spawns {
		t.Fatalf("m1 = %+v spawns %d→%d", got, spawns, f.sessions.spawnCount())
	}
	msgs := sentTo(f, m1.SessionID)
	if len(msgs) != 1 || !strings.Contains(msgs[0], `<ao-qa-defect plan="`+string(id)+`" milestone="m1">`) || !strings.Contains(msgs[0], "POST /todos returns 500") {
		t.Fatalf("defect messages = %v", msgs)
	}
	if d, _ := f.svc.Get(ctx, id); d.Phase != domain.PlanPhaseFixing {
		t.Fatalf("phase = %s", d.Phase)
	}
	if !strings.Contains(strings.Join(eventKinds(t, f, id), ","), "qa_failed,defect_sent") {
		t.Fatalf("events = %v", eventKinds(t, f, id))
	}
}

// Review Focus 3: a refused delivery leaves the milestone ready and is retried
// next tick, never with a second worker.
func TestRefusedDefectDeliveryIsRetriedWithoutASecondWorker(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	m1 := milestones(t, f, id)["m1"]
	f.sessions.sendErrFor = map[domain.SessionID]error{m1.SessionID: apierr.Conflict("SESSION_AWAITING_DECISION", "busy", nil)}
	spawns := f.sessions.spawnCount()
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "m1", "broken")); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	tick(t, f)
	if got := milestones(t, f, id)["m1"]; got.Status != domain.MilestoneStatusReady || got.QAVerdict != domain.QAVerdictFail {
		t.Fatalf("refused m1 = %+v", got)
	}
	if f.sessions.spawnCount() != spawns {
		t.Fatal("a second worker started while the original lives")
	}
	f.sessions.mu.Lock()
	f.sessions.sendErrFor = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	if got := milestones(t, f, id)["m1"]; got.Status != domain.MilestoneStatusRunning || len(sentTo(f, m1.SessionID)) != 1 {
		t.Fatalf("after the session freed up m1 = %+v", got)
	}
}

// A defect for a worker whose session ended starts a new worker on the same
// branch, carrying the findings.
func TestQAFailForAnEndedWorkerContinuesItsBranch(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	m1 := milestones(t, f, id)["m1"]
	f.sessions.endSession(m1.SessionID)
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "m1", "broken")); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	cfg := lastSpawn(f)
	if cfg.PlanContext == nil || cfg.PlanContext.MilestoneKey != "m1" || cfg.Branch != "ao/w-1/root" || cfg.BaseBranch != "" || cfg.RequireNewBranch ||
		!strings.Contains(cfg.Prompt, "<ao-qa-defect") {
		t.Fatalf("respawn = %+v", cfg)
	}
	if got := milestones(t, f, id)["m1"]; got.Status != domain.MilestoneStatusRunning || got.Attempt != 2 || got.SessionID == m1.SessionID {
		t.Fatalf("m1 = %+v", got)
	}
}

// QA records one fail per milestone, may not also pass, and runs again as a
// new session only after every failed milestone is done; the new round clears
// the old verdicts.
func TestQARoundWithSeveralFailsRerunsAfterAllFixes(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	for _, key := range []string{"m1", "m2"} {
		if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, key, key+" broken")); err != nil {
			t.Fatalf("fail %s: %v", key, err)
		}
	}
	_, err := f.svc.RecordQAResult(ctx, id, planQAPass(qaSess))
	if ae := wantCode(t, err, "PLAN_QA_HAS_FAILURES"); ae == nil {
		t.Fatal("pass after fails accepted")
	}
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: qaSess, State: domain.ReportDone, Note: "two defects"})
	tick(t, f)
	if qa := milestones(t, f, id)["qa"]; qa.Status != domain.MilestoneStatusPending {
		t.Fatalf("qa after its done report = %s", qa.Status)
	}
	reportDone(t, f, id, "m1", "fixed m1")
	if qa := milestones(t, f, id)["qa"]; qa.Status != domain.MilestoneStatusPending {
		t.Fatalf("qa re-promoted before m2 was fixed: %s", qa.Status)
	}
	reportDone(t, f, id, "m2", "fixed m2")
	ms := milestones(t, f, id)
	qa := ms["qa"]
	if qa.Status != domain.MilestoneStatusRunning || qa.SessionID == qaSess || qa.Attempt != 2 {
		t.Fatalf("second QA round = %+v", qa)
	}
	if ms["m1"].QAVerdict != domain.QAVerdictNone || ms["m2"].QAVerdict != domain.QAVerdictNone {
		t.Fatalf("verdicts not reset: %q %q", ms["m1"].QAVerdict, ms["m2"].QAVerdict)
	}
	// The old QA session may not record for the new round.
	_, err = f.svc.RecordQAResult(ctx, id, planQAPass(qaSess))
	wantCode(t, err, "PLAN_QA_SESSION_REQUIRED")
	if _, err := f.svc.RecordQAResult(ctx, id, planQAPass(qa.SessionID)); err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	if d, _ := f.svc.Get(ctx, id); d.Phase != domain.PlanPhaseDone {
		t.Fatalf("phase = %s", d.Phase)
	}
}

// A failure no milestone owns blocks QA for a person, even after its done
// report; retrying the qa milestone starts a new round.
func TestUnownedQAFailureWaitsForAPerson(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id, qaSess := toQA(t, f)
	if _, err := f.svc.RecordQAResult(ctx, id, planQAFail(qaSess, "", "the build is broken on main")); err != nil {
		t.Fatal(err)
	}
	if msgs := sentTo(f, "orch-1"); !strings.Contains(msgs[len(msgs)-1], "the build is broken on main") {
		t.Fatalf("orchestrator not told: %v", msgs)
	}
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: qaSess, State: domain.ReportDone})
	spawns := f.sessions.spawnCount()
	tick(t, f)
	if qa := milestones(t, f, id)["qa"]; qa.Status != domain.MilestoneStatusBlocked || f.sessions.spawnCount() != spawns {
		t.Fatalf("qa = %s spawns %d→%d", qa.Status, spawns, f.sessions.spawnCount())
	}
	if _, err := f.svc.RetryMilestone(ctx, id, "qa"); err != nil {
		t.Fatal(err)
	}
	if qa := milestones(t, f, id)["qa"]; qa.Status != domain.MilestoneStatusRunning || qa.SessionID == qaSess {
		t.Fatalf("retried qa = %+v", qa)
	}
}
