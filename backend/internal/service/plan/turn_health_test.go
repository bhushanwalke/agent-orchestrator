package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sqlite "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// fakeConversations is the chat side of a plan session: its thread state and
// its newest turns. A session with neither has no conversation (a TUI agent).
type fakeConversations struct {
	thread map[domain.SessionID]domain.ThreadStatus
	recent map[domain.SessionID][]domain.ConversationTurn
}

func (f *fakeConversations) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	status, hasThread := f.thread[id]
	if _, hasTurns := f.recent[id]; !hasThread && !hasTurns {
		return domain.ConversationRecord{}, sqlite.ErrConversationNotFound
	}
	return domain.ConversationRecord{ID: "conv-" + string(id), ThreadState: &domain.ConversationThreadState{Status: status}}, nil
}

func (f *fakeConversations) RecentConversationTurns(_ context.Context, conversationID string, limit int) ([]domain.ConversationTurn, error) {
	turns := f.recent[domain.SessionID(strings.TrimPrefix(conversationID, "conv-"))]
	if len(turns) > limit {
		turns = turns[:limit]
	}
	return turns, nil
}

func failedTurn(msg string) domain.ConversationTurn {
	return domain.ConversationTurn{State: domain.TurnStateFailed, ErrorMessage: msg}
}

func TestRunningMilestoneWithFailingTurnsIsBlockedUntilItReports(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2"}, msSpec{key: "m3"})
	ms := milestones(t, f, id)
	// m1: the provider gave up on the thread. m2: its last two turns failed.
	// m3: one failed turn after a good one is not yet a pattern.
	f.turns.thread[ms["m1"].SessionID] = domain.ThreadStatusSystemError
	f.turns.recent[ms["m2"].SessionID] = []domain.ConversationTurn{failedTurn("rate limited"), failedTurn("rate limited")}
	f.turns.recent[ms["m3"].SessionID] = []domain.ConversationTurn{failedTurn("boom"), {State: domain.TurnStateCompleted}}
	tick(t, f)

	ms = milestones(t, f, id)
	if ms["m1"].Status != domain.MilestoneStatusBlocked || ms["m2"].Status != domain.MilestoneStatusBlocked {
		t.Fatalf("m1=%s m2=%s, want both blocked", ms["m1"].Status, ms["m2"].Status)
	}
	if ms["m3"].Status != domain.MilestoneStatusRunning {
		t.Fatalf("m3=%s, want running", ms["m3"].Status)
	}
	events := planEvents(t, f, id, "milestone_blocked")
	if len(events) != 2 || !strings.Contains(strings.Join(events, "\n"), "agent turns are failing: rate limited") ||
		!strings.Contains(strings.Join(events, "\n"), "agent turns are failing: system_error") {
		t.Fatalf("blocked events = %q", events)
	}
	// A second tick does not record the block again.
	tick(t, f)
	if got := planEvents(t, f, id, "milestone_blocked"); len(got) != 2 {
		t.Fatalf("blocked events after second tick = %q", got)
	}
	// The worker recovers and checkpoints: running again.
	delete(f.turns.recent, ms["m2"].SessionID)
	f.svc.OnReport(context.Background(), domain.ReportRecord{SessionID: ms["m2"].SessionID, State: domain.ReportCheckpoint, Note: "back"})
	if got := milestones(t, f, id)["m2"].Status; got != domain.MilestoneStatusRunning {
		t.Fatalf("m2 after checkpoint = %s", got)
	}
}

func planEvents(t *testing.T, f fixture, id domain.PlanID, kind string) []string {
	t.Helper()
	d, err := f.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range d.Events {
		if e.Kind == kind {
			out = append(out, e.Note)
		}
	}
	return out
}

func TestFailingTurnsBlockCarriesTheProvidersMessage(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2"})
	ms := milestones(t, f, id)
	limit := "You've hit your usage limit. Upgrade or try again at 7:44 AM."
	f.turns.thread[ms["m1"].SessionID] = domain.ThreadStatusSystemError
	f.turns.recent[ms["m1"].SessionID] = []domain.ConversationTurn{failedTurn(limit)}
	long := strings.Repeat("x", 400)
	f.turns.recent[ms["m2"].SessionID] = []domain.ConversationTurn{failedTurn(long), failedTurn("older")}
	tick(t, f)
	notes := strings.Join(planEvents(t, f, id, "milestone_blocked"), "\n")
	if !strings.Contains(notes, "agent turns are failing: system_error: "+limit) {
		t.Fatalf("system_error note lacks the provider message: %q", notes)
	}
	for _, n := range planEvents(t, f, id, "milestone_blocked") {
		if strings.Contains(n, "xxx") && len(n) > len("agent turns are failing: ")+300+len("…") {
			t.Fatalf("message not clipped to 300: %d bytes", len(n))
		}
	}
}
