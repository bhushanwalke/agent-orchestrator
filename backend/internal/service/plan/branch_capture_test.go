package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The worker commits on a topic branch (AO's standing rule), and the plan
// records that branch at done time for its dependents and QA.
func TestDoneRecordsTheTopicBranchTheWorkerIsOn(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2", deps: []string{"m1"}})
	m1 := milestones(t, f, id)["m1"]
	// The session row still names its initial branch; the worktree moved on.
	f.workspaces.checkedOut["/wt/"+string(m1.SessionID)] = "ao/w-1/add-api"
	reportDone(t, f, id, "m1", "built")
	if got := milestones(t, f, id)["m1"].Branch; got != "ao/w-1/add-api" {
		t.Fatalf("stored branch = %q", got)
	}
	m2 := lastSpawn(f)
	if m2.BaseBranch != "ao/w-1/add-api" || !strings.Contains(m2.Prompt, "git merge ao/w-1/add-api") || strings.Contains(m2.Prompt, "ao/w-1/root") {
		t.Fatalf("m2 base=%q prompt:\n%s", m2.BaseBranch, m2.Prompt)
	}
}

func TestReportBranchWinsOverTheWorktree(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	id := activePlan(t, f, 3, msSpec{key: "m1"})
	m1 := milestones(t, f, id)["m1"]
	f.workspaces.checkedOut["/wt/"+string(m1.SessionID)] = "detached-elsewhere"
	f.svc.OnReport(context.Background(), domain.ReportRecord{SessionID: m1.SessionID, State: domain.ReportDone, Branch: "ao/w-1/declared"})
	if got := milestones(t, f, id)["m1"].Branch; got != "ao/w-1/declared" {
		t.Fatalf("stored branch = %q", got)
	}
}

func TestBranchFallsBackToThePRHeadThenTheSession(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	id := activePlan(t, f, 3, msSpec{key: "m1"}, msSpec{key: "m2"})
	ms := milestones(t, f, id)
	for _, key := range []string{"m1", "m2"} {
		f.workspaces.failFor["/wt/"+string(ms[key].SessionID)] = true
	}
	// m1 opened a PR from its topic branch.
	setSession(f, ms["m1"].SessionID, func(s *domain.Session) {
		s.PRs = []domain.PRFacts{{URL: "https://github.com/o/r/pull/9", Number: 9, SourceBranch: "ao/w-1/from-pr"}}
	})
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: ms["m1"].SessionID, State: domain.ReportDone,
		Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/9"}}})
	f.svc.OnReport(ctx, domain.ReportRecord{SessionID: ms["m2"].SessionID, State: domain.ReportDone})
	got := milestones(t, f, id)
	if got["m1"].Branch != "ao/w-1/from-pr" || got["m2"].Branch != "ao/w-2/root" {
		t.Fatalf("branches = %q / %q", got["m1"].Branch, got["m2"].Branch)
	}
	evs, _ := f.store.ListPlanEvents(ctx, id, 0, 100)
	unresolved := 0
	for _, e := range evs {
		if e.Kind == "milestone_branch_unresolved" {
			unresolved++
			if e.MilestoneID != ms["m2"].ID || !strings.Contains(e.Note, "ao/w-2/root") {
				t.Fatalf("unresolved event = %+v", e)
			}
		}
	}
	if unresolved != 1 {
		t.Fatalf("milestone_branch_unresolved events = %d", unresolved)
	}
}
