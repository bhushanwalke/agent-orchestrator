package controllers

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	plansvc "github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

// The milestone's branch is the one AO recorded when its work reported done;
// the session's own branch is only a fallback before then.
func TestMilestoneResponseBranchPrefersTheRecordedBranch(t *testing.T) {
	sess := &domain.Session{}
	sess.ID = "w-1"
	sess.Metadata.Branch = "ao/topic-one/root"
	done := milestoneResponse(plansvc.MilestoneView{Milestone: domain.Milestone{Key: "m1", Branch: "topic/one"}, Session: sess})
	if done.Branch != "topic/one" {
		t.Fatalf("recorded branch: got %q", done.Branch)
	}
	running := milestoneResponse(plansvc.MilestoneView{Milestone: domain.Milestone{Key: "m2"}, Session: sess})
	if running.Branch != "ao/topic-one/root" {
		t.Fatalf("session fallback: got %q", running.Branch)
	}
}
