package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Review Focus 1: a report from a session that owns no milestone is a no-op.
func TestOnReportIgnoresSessionsOutsidePlans(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	before := strings.Join(eventKinds(t, f, id), ",")
	f.svc.OnReport(context.Background(), domain.ReportRecord{SessionID: "some-other-worker", State: domain.ReportDone, Note: "x"})
	if got := strings.Join(eventKinds(t, f, id), ","); got != before {
		t.Fatalf("events changed: %s", got)
	}
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusRunning {
		t.Fatalf("m1 = %s", m.Status)
	}
}

func TestOnReportBlocksAndResumesMilestones(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	sess := milestones(t, f, id)["m1"].SessionID
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: sess, State: domain.ReportStuck, Note: "need creds"})
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusBlocked {
		t.Fatalf("after stuck m1 = %s", m.Status)
	}
	d, _ := f.svc.Get(ctx, id)
	if d.DisplayStatus != "Blocked" {
		t.Fatalf("display = %s", d.DisplayStatus)
	}
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: sess, State: domain.ReportCheckpoint, Note: "unblocked"})
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusRunning {
		t.Fatalf("after checkpoint m1 = %s", m.Status)
	}
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: sess, State: domain.ReportNeedsInput})
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: sess, State: domain.ReportDone, Note: "built"})
	if m := milestones(t, f, id)["m1"]; m.Status != domain.MilestoneStatusDone || m.FinishedAt == nil {
		t.Fatalf("blocked then done m1 = %+v", m)
	}
	kinds := strings.Join(eventKinds(t, f, id), ",")
	for _, want := range []string{"milestone_blocked", "milestone_checkpoint", "milestone_done"} {
		if !strings.Contains(kinds, want) {
			t.Fatalf("events missing %s: %s", want, kinds)
		}
	}
	// A done report wakes the scheduler.
	select {
	case <-f.svc.WakeC():
	default:
		t.Fatal("done report did not wake the scheduler")
	}
}

func TestQADoneWithoutVerdictFailsAndAsksForOne(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	reportDone(t, f, id, "m1", "ok")
	qa := milestones(t, f, id)["qa"]
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: qa.SessionID, State: domain.ReportDone, Note: "looked fine"})
	if m := milestones(t, f, id)["qa"]; m.Status != domain.MilestoneStatusFailed {
		t.Fatalf("qa = %s", m.Status)
	}
	// The request goes out from the scheduler, never inside the report call.
	tick(t, f)
	f.sessions.mu.Lock()
	last := f.sessions.sent[len(f.sessions.sent)-1]
	f.sessions.mu.Unlock()
	if last.ID != qa.SessionID || !strings.Contains(last.Text, "ao plan qa-result "+string(id)) {
		t.Fatalf("message = %+v", last)
	}
	// The QA session is alive, so no second QA starts; it may still answer.
	spawns := f.sessions.spawnCount()
	tick(t, f)
	if f.sessions.spawnCount() != spawns || len(sentTo(f, qa.SessionID)) != 1 {
		t.Fatal("a second QA spawned, or the request was repeated, while the first can still answer")
	}
	if _, err := f.svc.RecordQAResult(ctx, id, planQAPass(qa.SessionID)); err != nil {
		t.Fatalf("late verdict refused: %v", err)
	}
	tick(t, f)
	if d, _ := f.svc.Get(ctx, id); d.Phase != domain.PlanPhaseDone {
		t.Fatalf("phase = %s", d.Phase)
	}
}
