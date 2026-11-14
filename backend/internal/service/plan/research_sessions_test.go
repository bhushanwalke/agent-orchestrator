package plan_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDetailListsResearchSessionsAttachedWithoutMilestone(t *testing.T) {
	f := newFixture(t, domain.ProjectKindSingleRepo)
	p := createPlan(t, f)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	create := func(milestone domain.MilestoneID) domain.SessionID {
		t.Helper()
		rec, err := f.store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			Metadata: domain.SessionMetadata{Branch: "ao/research/root", WorkspacePath: "/ws"},
			PlanID:   p.ID, PlanMilestoneID: milestone, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		f.sessions.sessions[rec.ID] = domain.Session{SessionRecord: rec}
		return rec.ID
	}
	research := create("")
	create("ms_x")

	d, err := f.svc.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(d.ResearchSessions) != 1 || d.ResearchSessions[0].ID != research ||
		d.ResearchSessions[0].Metadata.Branch != "ao/research/root" {
		t.Fatalf("research sessions = %+v", d.ResearchSessions)
	}
}
