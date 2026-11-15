package contract_test

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// finishedWorker is a settled worker with commits and no PR in a project with
// no PR provider: the case Needs you exists for.
func finishedWorker(activity contract.ActivityState) contract.KanbanSessionFacts {
	session := sessionAt(activity)
	session.Commits = 2
	return session
}

func with(session contract.KanbanSessionFacts, edit func(*contract.KanbanSessionFacts)) contract.KanbanSessionFacts {
	edit(&session)
	return session
}

func TestDeriveKanbanPresentationLanes(t *testing.T) {
	t.Parallel()
	inReview := contract.KanbanPRFacts{URL: "pr/1"}
	ciFailing := contract.KanbanPRFacts{URL: "pr/1", CI: contract.CIFailing}
	conflicting := contract.KanbanPRFacts{URL: "pr/1", Mergeability: contract.MergeConflicting}
	changesRequested := contract.KanbanPRFacts{URL: "pr/1", ExternalReview: contract.KanbanExternalReviewFacts{ChangesRequested: true}}
	commented := contract.KanbanPRFacts{URL: "pr/1", ExternalReview: contract.KanbanExternalReviewFacts{Comments: true}}
	merged := contract.KanbanPRFacts{URL: "pr/1", Merged: true}

	tests := []struct {
		name    string
		session contract.KanbanSessionFacts
		prs     []contract.KanbanPRFacts
		want    contract.KanbanPresentation
	}{
		{
			name: "terminated outranks every request",
			session: with(finishedWorker(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) {
				s.IsTerminated = true
				s.BoardRequest = contract.BoardRequestAttention
			}),
			want: contract.KanbanPresentation{Column: contract.KanbanArchive, DisplayStatus: contract.DisplayTerminated, Stage: contract.KanbanArchive},
		},
		{
			name:    "accepted with no PR is ready",
			session: with(finishedWorker(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.BoardRequest = contract.BoardRequestAccepted }),
			want:    contract.KanbanPresentation{Column: contract.KanbanReady, DisplayStatus: contract.DisplayAccepted, Stage: contract.KanbanBuilding},
		},
		{
			name:    "accepted is ignored once a PR speaks for the session",
			session: with(sessionAt(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.BoardRequest = contract.BoardRequestAccepted }),
			prs:     []contract.KanbanPRFacts{commented},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsReview, DisplayStatus: contract.DisplayCommented, Stage: contract.KanbanNeedsReview},
		},
		{
			name: "accepted outranks provisioning",
			session: with(sessionAt(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) {
				s.Provisioning = true
				s.BoardRequest = contract.BoardRequestAccepted
			}),
			want: contract.KanbanPresentation{Column: contract.KanbanReady, DisplayStatus: contract.DisplayAccepted, Stage: contract.KanbanBuilding},
		},
		{
			name: "a provisioning session is starting in the queue",
			session: with(sessionAt(contract.ActivityActive), func(s *contract.KanbanSessionFacts) {
				s.Provisioning = true
				s.BoardRequest = contract.BoardRequestAttention
			}),
			want: contract.KanbanPresentation{Column: contract.KanbanQueued, DisplayStatus: contract.DisplayStarting, Stage: contract.KanbanQueued},
		},
		{
			name:    "the attention request pauses a working session",
			session: with(sessionAt(contract.ActivityActive), func(s *contract.KanbanSessionFacts) { s.BoardRequest = contract.BoardRequestAttention }),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayPausedForYou, Stage: contract.KanbanBuilding},
		},
		{
			name:    "the attention request keeps the PR stage",
			session: with(sessionAt(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.BoardRequest = contract.BoardRequestAttention }),
			prs:     []contract.KanbanPRFacts{changesRequested},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayPausedForYou, Stage: contract.KanbanNeedsReview},
		},
		{
			name:    "a blocked worker needs you",
			session: sessionAt(contract.ActivityBlocked),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayBlocked, Stage: contract.KanbanBuilding},
		},
		{
			name:    "a worker asking a question needs you from validating",
			session: with(sessionAt(contract.ActivityWaitingInput), func(s *contract.KanbanSessionFacts) { s.AutoInjectCI = true }),
			prs:     []contract.KanbanPRFacts{ciFailing},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayNeedsInput, Stage: contract.KanbanValidating},
		},
		{
			name:    "failing CI nobody is fixing needs you",
			session: sessionAt(contract.ActivityIdle),
			prs:     []contract.KanbanPRFacts{ciFailing},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayCIFailing, Stage: contract.KanbanNeedsReview},
		},
		{
			name:    "failing CI AO is fixing stays validating",
			session: with(sessionAt(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.AutoInjectCI = true }),
			prs:     []contract.KanbanPRFacts{ciFailing},
			want:    contract.KanbanPresentation{Column: contract.KanbanValidating, DisplayStatus: contract.DisplayCIFailing, Stage: contract.KanbanValidating},
		},
		{
			name:    "merge conflicts in review need you",
			session: sessionAt(contract.ActivityIdle),
			prs:     []contract.KanbanPRFacts{conflicting},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayMergeConflicts, Stage: contract.KanbanNeedsReview},
		},
		{
			name:    "a person's changes request needs you",
			session: sessionAt(contract.ActivityIdle),
			prs:     []contract.KanbanPRFacts{changesRequested},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayChangesRequested, Stage: contract.KanbanNeedsReview},
		},
		{
			name:    "comments stay in review (R7)",
			session: sessionAt(contract.ActivityIdle),
			prs:     []contract.KanbanPRFacts{commented},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsReview, DisplayStatus: contract.DisplayCommented, Stage: contract.KanbanNeedsReview},
		},
		{
			name:    "awaiting review with a PR stays in review (R7)",
			session: sessionAt(contract.ActivityIdle),
			prs:     []contract.KanbanPRFacts{inReview},
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsReview, DisplayStatus: contract.DisplayAwaitingReview, Stage: contract.KanbanNeedsReview},
		},
		{
			name:    "a settled worker reporting it needs input needs you",
			session: with(sessionAt(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.LatestReport = contract.ReportNeedsInput }),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayNeedsInput, Stage: contract.KanbanBuilding},
		},
		{
			name:    "an exited worker reporting it is stuck needs you",
			session: with(sessionAt(contract.ActivityExited), func(s *contract.KanbanSessionFacts) { s.LatestReport = contract.ReportStuck }),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayStuck, Stage: contract.KanbanBuilding},
		},
		{
			name:    "a stuck report from a worker still working does not",
			session: with(sessionAt(contract.ActivityActive), func(s *contract.KanbanSessionFacts) { s.LatestReport = contract.ReportStuck }),
			want:    contract.KanbanPresentation{Column: contract.KanbanBuilding, DisplayStatus: contract.DisplayWorking, Stage: contract.KanbanBuilding},
		},
		{
			name:    "finished work with nowhere to deliver awaits review",
			session: finishedWorker(contract.ActivityIdle),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayAwaitingReview, Stage: contract.KanbanBuilding},
		},
		{
			name:    "an exited worker with commits and no provider awaits review",
			session: finishedWorker(contract.ActivityExited),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayAwaitingReview, Stage: contract.KanbanBuilding},
		},
		{
			name:    "exited alone is not needs you (R2)",
			session: sessionAt(contract.ActivityExited),
			want:    contract.KanbanPresentation{Column: contract.KanbanBuilding, DisplayStatus: contract.DisplayExited, Stage: contract.KanbanBuilding},
		},
		{
			name:    "a finished worker with a PR provider stays building",
			session: with(finishedWorker(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.HasPRProvider = true }),
			want:    contract.KanbanPresentation{Column: contract.KanbanBuilding, DisplayStatus: contract.DisplayIdle, Stage: contract.KanbanBuilding},
		},
		{
			name:    "a plan-managed finished worker stays building",
			session: with(finishedWorker(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.PlanManaged = true }),
			want:    contract.KanbanPresentation{Column: contract.KanbanBuilding, DisplayStatus: contract.DisplayIdle, Stage: contract.KanbanBuilding},
		},
		{
			name:    "a plan-managed worker asking a question still needs you",
			session: with(sessionAt(contract.ActivityWaitingInput), func(s *contract.KanbanSessionFacts) { s.PlanManaged = true }),
			want:    contract.KanbanPresentation{Column: contract.KanbanNeedsYou, DisplayStatus: contract.DisplayNeedsInput, Stage: contract.KanbanBuilding},
		},
		{
			name:    "an interrupted finished worker stays building",
			session: with(finishedWorker(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.Interrupted = true }),
			want:    contract.KanbanPresentation{Column: contract.KanbanBuilding, DisplayStatus: contract.DisplayInterrupted, Stage: contract.KanbanBuilding},
		},
		{
			name:    "a finished worker with no commits stays building",
			session: sessionAt(contract.ActivityIdle),
			want:    contract.KanbanPresentation{Column: contract.KanbanBuilding, DisplayStatus: contract.DisplayIdle, Stage: contract.KanbanBuilding},
		},
		{
			name:    "artifact output is not finished-with-nowhere-to-deliver",
			session: with(finishedWorker(contract.ActivityIdle), func(s *contract.KanbanSessionFacts) { s.HasArtifactOutput = true }),
			want:    contract.KanbanPresentation{Column: contract.KanbanValidating, DisplayStatus: contract.DisplayAwaitingReview, Stage: contract.KanbanValidating},
		},
		{
			name:    "a merged PR stays ready",
			session: finishedWorker(contract.ActivityIdle),
			prs:     []contract.KanbanPRFacts{merged},
			want:    contract.KanbanPresentation{Column: contract.KanbanReady, DisplayStatus: contract.DisplayMerged, Stage: contract.KanbanReady},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := contract.DeriveKanbanPresentation(tc.session, tc.prs, time.Unix(3600, 0), testGrace)
			if got != tc.want {
				t.Fatalf("presentation = %+v, want %+v", got, tc.want)
			}
		})
	}
}
