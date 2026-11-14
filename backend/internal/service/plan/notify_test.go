package plan_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// noticesTo returns the texts delivered to one session that contain marker.
func noticesTo(f fixture, id domain.SessionID, marker string) []string {
	f.sessions.mu.Lock()
	defer f.sessions.mu.Unlock()
	var out []string
	for _, m := range f.sessions.sent {
		if m.ID == id && strings.Contains(m.Text, marker) {
			out = append(out, m.Text)
		}
	}
	return out
}

func report(f fixture, sess domain.SessionID, state domain.ReportState, note string) {
	f.svc.OnReport(context.Background(), domain.ReportRecord{SessionID: sess, State: state, Note: note})
}

func TestStuckMilestoneTellsTheOrchestratorOnce(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	m1 := milestones(t, f, id)["m1"]
	report(f, m1.SessionID, domain.ReportStuck, "Which database should the store use?")
	tick(t, f)
	tick(t, f)

	notices := noticesTo(f, "orch-1", "<ao-milestone-blocked")
	if len(notices) != 1 {
		t.Fatalf("blocked notices = %d, want 1: %q", len(notices), notices)
	}
	n := notices[0]
	for _, want := range []string{`plan="` + string(id) + `"`, `milestone="m1"`, `session="` + string(m1.SessionID) + `"`,
		"Which database should the store use?", "ao send --session " + string(m1.SessionID), "ao plan cancel " + string(id)} {
		if !strings.Contains(n, want) {
			t.Fatalf("notice lacks %q:\n%s", want, n)
		}
	}
	if got := planEvents(t, f, id, "milestone_blocked_notified"); len(got) != 1 {
		t.Fatalf("notified events = %q", got)
	}
	// A second block episode is a second notice.
	report(f, m1.SessionID, domain.ReportCheckpoint, "back")
	report(f, m1.SessionID, domain.ReportNeedsInput, "Need the API key name")
	tick(t, f)
	if got := noticesTo(f, "orch-1", "<ao-milestone-blocked"); len(got) != 2 || !strings.Contains(got[1], "Need the API key name") {
		t.Fatalf("second episode notices = %q", got)
	}
}

func TestBlockedNoticeWaitsForABusyOrchestrator(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	m1 := milestones(t, f, id)["m1"]
	f.sessions.mu.Lock()
	f.sessions.sendErrFor = map[domain.SessionID]error{"orch-1": errors.New("orchestrator is busy")}
	f.sessions.mu.Unlock()
	report(f, m1.SessionID, domain.ReportStuck, "blocked on a decision")
	tick(t, f)
	if got := planEvents(t, f, id, "milestone_blocked_notified"); len(got) != 0 {
		t.Fatalf("notified while refused: %q", got)
	}
	// The worker unblocks itself before the orchestrator is free: the notice
	// is still delivered, and says so.
	report(f, m1.SessionID, domain.ReportCheckpoint, "worked it out")
	f.sessions.mu.Lock()
	f.sessions.sendErrFor = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	notices := noticesTo(f, "orch-1", "<ao-milestone-blocked")
	if len(notices) != 1 || !strings.Contains(notices[0], "blocked on a decision") || !strings.Contains(notices[0], "running again") {
		t.Fatalf("late notice = %q", notices)
	}
	if got := planEvents(t, f, id, "milestone_blocked_notified"); len(got) != 1 || !strings.Contains(got[0], "running again") {
		t.Fatalf("notified events = %q", got)
	}
}

func TestMilestoneOutOfAttemptsTellsTheOrchestrator(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	for i := 0; i < domain.MaxMilestoneAttempts; i++ {
		m1 := milestones(t, f, id)["m1"]
		if m1.Status != domain.MilestoneStatusRunning {
			t.Fatalf("attempt %d: m1 is %s", i+1, m1.Status)
		}
		f.sessions.endSession(m1.SessionID)
		tick(t, f)
	}
	m1 := milestones(t, f, id)["m1"]
	if m1.Status != domain.MilestoneStatusFailed {
		t.Fatalf("m1 = %s, want failed", m1.Status)
	}
	tick(t, f)
	notices := noticesTo(f, "orch-1", "<ao-milestone-failed")
	if len(notices) != 1 {
		t.Fatalf("failed notices = %q", notices)
	}
	for _, want := range []string{`milestone="m1"`, "ended without reporting done", "ao plan milestone retry " + string(id) + " m1"} {
		if !strings.Contains(notices[0], want) {
			t.Fatalf("notice lacks %q:\n%s", want, notices[0])
		}
	}
	// Failures with attempts left are retried silently.
	if got := noticesTo(f, "orch-1", "<ao-milestone-blocked"); len(got) != 0 {
		t.Fatalf("unexpected blocked notices %q", got)
	}
}
