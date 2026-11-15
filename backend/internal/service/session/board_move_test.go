package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func (f *fakeStore) SetSessionBoardRequest(_ context.Context, id domain.SessionID, request domain.BoardRequest, at time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.BoardRequest = request
	r.BoardRequestAt = nil
	if request != domain.BoardRequestNone {
		r.BoardRequestAt = &at
	}
	f.sessions[id] = r
	return true, nil
}

type fakeBoardReviewer struct {
	triggered []string
	cancelled []domain.SessionID
	err       error
}

func (f *fakeBoardReviewer) TriggerBoardReview(_ context.Context, worker domain.SessionID, prURL string) error {
	f.triggered = append(f.triggered, string(worker)+" "+prURL)
	return f.err
}

func (f *fakeBoardReviewer) CancelRunningReview(_ context.Context, worker domain.SessionID) error {
	f.cancelled = append(f.cancelled, worker)
	return nil
}

var boardNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// boardCase is one starting state of the board-move matrix.
type boardCase struct {
	rec      domain.SessionRecord
	prs      []domain.PRFacts
	provider bool
}

func boardWorker(activity domain.ActivityState, commits int) domain.SessionRecord {
	return domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Activity:    domain.Activity{State: activity, LastActivityAt: boardNow},
		BranchState: &domain.SessionBranchState{Commits: commits, Base: "main"},
	}
}

var boardStates = map[string]func() boardCase{
	// Finished, no PR, no provider: Needs you as Awaiting review.
	"needs_you by fact, no PR": func() boardCase {
		return boardCase{rec: boardWorker(domain.ActivityIdle, 2)}
	},
	"building, provider": func() boardCase {
		return boardCase{rec: boardWorker(domain.ActivityActive, 1), provider: true}
	},
	"PR validating": func() boardCase {
		rec := boardWorker(domain.ActivityActive, 1)
		rec.AutoInjectCI = true
		return boardCase{rec: rec, provider: true, prs: []domain.PRFacts{{URL: "https://github.com/o/r/pull/1", CI: domain.CIFailing}}}
	},
	"PR needs_review": func() boardCase {
		return boardCase{rec: boardWorker(domain.ActivityIdle, 1), provider: true, prs: []domain.PRFacts{{URL: "https://github.com/o/r/pull/1"}}}
	},
	"needs_you by fact, PR": func() boardCase {
		return boardCase{rec: boardWorker(domain.ActivityIdle, 1), provider: true, prs: []domain.PRFacts{{URL: "https://github.com/o/r/pull/1", ExternalChangesRequested: true}}}
	},
	"needs_you by request": func() boardCase {
		rec := boardWorker(domain.ActivityActive, 1)
		rec.BoardRequest = domain.BoardRequestAttention
		return boardCase{rec: rec, provider: true}
	},
	"merged PR": func() boardCase {
		return boardCase{rec: boardWorker(domain.ActivityIdle, 1), provider: true, prs: []domain.PRFacts{{URL: "https://github.com/o/r/pull/1", Merged: true}}}
	},
	"accepted": func() boardCase {
		rec := boardWorker(domain.ActivityIdle, 2)
		rec.BoardRequest = domain.BoardRequestAccepted
		return boardCase{rec: rec}
	},
	"draft PR": func() boardCase {
		rec := boardWorker(domain.ActivityIdle, 1)
		return boardCase{rec: rec, provider: true, prs: []domain.PRFacts{{URL: "https://github.com/o/r/pull/1", Draft: true}}}
	},
}

func newBoardService(c boardCase) (*Service, *fakeStore, *fakeCommander, *fakeBoardReviewer) {
	st := newFakeStore()
	st.sessions[c.rec.ID] = c.rec
	if len(c.prs) > 0 {
		st.prFacts[c.rec.ID] = c.prs
	}
	origin := ""
	if c.provider {
		origin = "https://github.com/o/r.git"
	}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: origin}
	commander := &fakeCommander{}
	reviewer := &fakeBoardReviewer{}
	svc := &Service{store: st, manager: commander, scm: fakeSCM{}, clock: func() time.Time { return boardNow }}
	svc.SetBoardReviewer(reviewer)
	return svc, st, commander, reviewer
}

func refusalReason(err error) string {
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		return ""
	}
	reason, _ := ae.Details["reason"].(string)
	return reason
}

func TestBoardMoveStartingStatesPlaceAsExpected(t *testing.T) {
	want := map[string][2]domain.KanbanColumn{
		"needs_you by fact, no PR": {domain.KanbanNeedsYou, domain.KanbanBuilding},
		"building, provider":       {domain.KanbanBuilding, domain.KanbanBuilding},
		"PR validating":            {domain.KanbanValidating, domain.KanbanValidating},
		"PR needs_review":          {domain.KanbanNeedsReview, domain.KanbanNeedsReview},
		"needs_you by fact, PR":    {domain.KanbanNeedsYou, domain.KanbanNeedsReview},
		"needs_you by request":     {domain.KanbanNeedsYou, domain.KanbanBuilding},
		"draft PR":                 {domain.KanbanValidating, domain.KanbanValidating},
		"merged PR":                {domain.KanbanReady, domain.KanbanReady},
		"accepted":                 {domain.KanbanReady, domain.KanbanBuilding},
	}
	for name, state := range boardStates {
		svc, _, _, _ := newBoardService(state())
		sess, err := svc.Get(context.Background(), "mer-1")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := [2]domain.KanbanColumn{sess.KanbanColumn, sess.KanbanStage}; got != want[name] {
			t.Errorf("%s: column/stage = %v, want %v", name, got, want[name])
		}
	}
}

func TestBoardMoveMatrix(t *testing.T) {
	type outcome struct {
		refusal string
		// notice is the tag of the one notice sent, or "".
		notice  string
		request domain.BoardRequest
		check   func(t *testing.T, st *fakeStore, rv *fakeBoardReviewer)
	}
	flags := func(review, ci, auto bool) func(*testing.T, *fakeStore, *fakeBoardReviewer) {
		return func(t *testing.T, st *fakeStore, _ *fakeBoardReviewer) {
			t.Helper()
			r := st.sessions["mer-1"]
			if r.AutoInjectReview != review || r.AutoInjectCI != ci || r.AutoReviewEnabled != auto {
				t.Fatalf("flags review/ci/auto = %v/%v/%v, want %v/%v/%v", r.AutoInjectReview, r.AutoInjectCI, r.AutoReviewEnabled, review, ci, auto)
			}
		}
	}
	reviewTriggered := func(t *testing.T, st *fakeStore, rv *fakeBoardReviewer) {
		t.Helper()
		flags(true, true, false)(t, st, rv)
		if len(rv.triggered) != 1 || rv.triggered[0] != "mer-1 https://github.com/o/r/pull/1" {
			t.Fatalf("review triggers = %v", rv.triggered)
		}
	}
	handedToPerson := func(t *testing.T, st *fakeStore, rv *fakeBoardReviewer) {
		t.Helper()
		flags(false, false, false)(t, st, rv)
		if len(rv.cancelled) != 1 {
			t.Fatalf("review cancels = %v, want one", rv.cancelled)
		}
	}
	cases := []struct {
		state string
		to    domain.KanbanColumn
		want  outcome
	}{
		{"needs_you by fact, no PR", domain.KanbanQueued, outcome{refusal: "session_cannot_queue"}},
		{"needs_you by fact, no PR", domain.KanbanBuilding, outcome{refusal: "already_building"}},
		{"needs_you by fact, no PR", domain.KanbanValidating, outcome{refusal: "no_pr_provider"}},
		{"needs_you by fact, no PR", domain.KanbanNeedsReview, outcome{refusal: "no_pr"}},
		{"needs_you by fact, no PR", domain.KanbanReady, outcome{request: domain.BoardRequestAccepted}},
		{"needs_you by fact, no PR", domain.KanbanNeedsYou, outcome{notice: "<ao-board-hold>", request: domain.BoardRequestAttention}},

		{"building, provider", domain.KanbanQueued, outcome{refusal: "session_cannot_queue"}},
		{"building, provider", domain.KanbanBuilding, outcome{refusal: "already_building"}},
		{"building, provider", domain.KanbanValidating, outcome{notice: "<ao-board-deliver>"}},
		{"building, provider", domain.KanbanNeedsReview, outcome{refusal: "no_pr"}},
		{"building, provider", domain.KanbanReady, outcome{request: domain.BoardRequestAccepted}},
		{"building, provider", domain.KanbanNeedsYou, outcome{notice: "<ao-board-hold>", request: domain.BoardRequestAttention}},

		{"PR validating", domain.KanbanQueued, outcome{refusal: "session_cannot_queue"}},
		{"PR validating", domain.KanbanBuilding, outcome{refusal: "cannot_restart"}},
		{"PR validating", domain.KanbanValidating, outcome{refusal: "already_there"}},
		{"PR validating", domain.KanbanNeedsReview, outcome{check: handedToPerson}},
		{"PR validating", domain.KanbanReady, outcome{refusal: "pr_decides_ready"}},
		{"PR validating", domain.KanbanNeedsYou, outcome{notice: "<ao-board-hold>", request: domain.BoardRequestAttention}},

		{"PR needs_review", domain.KanbanBuilding, outcome{refusal: "cannot_restart"}},
		{"PR needs_review", domain.KanbanValidating, outcome{check: reviewTriggered}},
		{"PR needs_review", domain.KanbanNeedsReview, outcome{refusal: "already_there"}},
		{"PR needs_review", domain.KanbanReady, outcome{refusal: "pr_decides_ready"}},
		{"PR needs_review", domain.KanbanNeedsYou, outcome{notice: "<ao-board-hold>", request: domain.BoardRequestAttention}},

		{"needs_you by fact, PR", domain.KanbanValidating, outcome{check: reviewTriggered}},
		{"needs_you by fact, PR", domain.KanbanNeedsReview, outcome{refusal: "already_there"}},
		{"needs_you by fact, PR", domain.KanbanNeedsYou, outcome{notice: "<ao-board-hold>", request: domain.BoardRequestAttention}},

		{"needs_you by request", domain.KanbanBuilding, outcome{notice: "<ao-board-resume>"}},
		{"needs_you by request", domain.KanbanQueued, outcome{refusal: "session_cannot_queue", request: domain.BoardRequestAttention}},
		{"needs_you by request", domain.KanbanNeedsYou, outcome{refusal: "already_there", request: domain.BoardRequestAttention}},
		{"needs_you by request", domain.KanbanReady, outcome{notice: "<ao-board-resume>", request: domain.BoardRequestAccepted}},
		{"needs_you by request", domain.KanbanNeedsReview, outcome{refusal: "no_pr", request: domain.BoardRequestAttention}},

		{"draft PR", domain.KanbanNeedsReview, outcome{refusal: "pr_is_draft"}},
		{"merged PR", domain.KanbanValidating, outcome{refusal: "pr_decides_ready"}},
		// R33: dropping accepted work on Building takes the acceptance back.
		{"accepted", domain.KanbanBuilding, outcome{}},
		{"accepted", domain.KanbanReady, outcome{refusal: "already_there", request: domain.BoardRequestAccepted}},
		{"draft PR", domain.KanbanValidating, outcome{refusal: "already_there"}},
	}
	for _, tc := range cases {
		t.Run(tc.state+" to "+string(tc.to), func(t *testing.T) {
			start := boardStates[tc.state]()
			svc, st, commander, reviewer := newBoardService(start)
			_, err := svc.BoardMove(context.Background(), "mer-1", tc.to)
			if got := refusalReason(err); got != tc.want.refusal {
				t.Fatalf("refusal = %q (err %v), want %q", got, err, tc.want.refusal)
			}
			if tc.want.refusal == "" && err != nil {
				t.Fatalf("move: %v", err)
			}
			switch {
			case tc.want.notice == "" && len(commander.sentMessages) > 0:
				t.Fatalf("notices = %q, want none", commander.sentMessages)
			case tc.want.notice != "" && (len(commander.sentMessages) != 1 || !strings.HasPrefix(commander.sentMessages[0], tc.want.notice)):
				t.Fatalf("notices = %q, want one %s", commander.sentMessages, tc.want.notice)
			}
			if got := st.sessions["mer-1"].BoardRequest; got != tc.want.request {
				t.Fatalf("board request = %q, want %q", got, tc.want.request)
			}
			if tc.want.refusal != "" {
				if st.sessions["mer-1"].AutoInjectCI != start.rec.AutoInjectCI || len(reviewer.triggered)+len(reviewer.cancelled) > 0 {
					t.Fatal("a refused move changed facts")
				}
			}
			if tc.want.check != nil {
				tc.want.check(t, st, reviewer)
			}
		})
	}
}

func TestBoardMoveLandsCardsWhereTheFactsPutThem(t *testing.T) {
	ctx := context.Background()

	svc, _, _, _ := newBoardService(boardStates["needs_you by fact, no PR"]())
	sess, err := svc.BoardMove(ctx, "mer-1", domain.KanbanReady)
	if err != nil || sess.KanbanColumn != domain.KanbanReady || sess.DisplayStatus != contract.DisplayAccepted {
		t.Fatalf("accept: %s/%s err=%v", sess.KanbanColumn, sess.DisplayStatus, err)
	}

	svc, _, _, _ = newBoardService(boardStates["building, provider"]())
	sess, err = svc.BoardMove(ctx, "mer-1", domain.KanbanNeedsYou)
	if err != nil || sess.KanbanColumn != domain.KanbanNeedsYou || sess.DisplayStatus != contract.DisplayPausedForYou || sess.KanbanStage != domain.KanbanBuilding {
		t.Fatalf("pause: %s/%s/%s err=%v", sess.KanbanColumn, sess.DisplayStatus, sess.KanbanStage, err)
	}
	sess, err = svc.BoardMove(ctx, "mer-1", domain.KanbanBuilding)
	if err != nil || sess.KanbanColumn != domain.KanbanBuilding {
		t.Fatalf("release: %s err=%v", sess.KanbanColumn, err)
	}

	// Handed to a person: with AO's loops off the PR's next turn is a person's.
	svc, _, _, _ = newBoardService(boardStates["PR validating"]())
	sess, err = svc.BoardMove(ctx, "mer-1", domain.KanbanNeedsReview)
	if err != nil || sess.KanbanStage != domain.KanbanNeedsReview {
		t.Fatalf("hand to person: %s/%s err=%v", sess.KanbanColumn, sess.KanbanStage, err)
	}
}

func TestBoardMoveNoticesNameTheLaneAndBaseBranch(t *testing.T) {
	ctx := context.Background()
	svc, _, commander, _ := newBoardService(boardStates["building, provider"]())
	if _, err := svc.BoardMove(ctx, "mer-1", domain.KanbanValidating); err != nil {
		t.Fatal(err)
	}
	deliver := commander.sentMessages[0]
	if !strings.Contains(deliver, "open a pull request against main") || !strings.HasSuffix(deliver, "</ao-board-deliver>") || len(deliver) > 1024 {
		t.Fatalf("deliver notice = %q", deliver)
	}
	svc, _, commander, _ = newBoardService(boardStates["needs_you by request"]())
	if _, err := svc.BoardMove(ctx, "mer-1", domain.KanbanBuilding); err != nil {
		t.Fatal(err)
	}
	if resume := commander.sentMessages[0]; !strings.Contains(resume, `back to the board's "Building" lane`) {
		t.Fatalf("resume notice = %q", resume)
	}
}

func TestBoardMoveRejectsUnknownLanesAndTerminatedSessions(t *testing.T) {
	ctx := context.Background()
	svc, st, _, _ := newBoardService(boardStates["building, provider"]())
	for _, to := range []domain.KanbanColumn{domain.KanbanArchive, "sideways"} {
		_, err := svc.BoardMove(ctx, "mer-1", to)
		var ae *apierr.Error
		if !errors.As(err, &ae) || ae.Code != "INVALID_BOARD_COLUMN" {
			t.Fatalf("to %q: err = %v, want INVALID_BOARD_COLUMN", to, err)
		}
	}
	if _, err := svc.BoardMove(ctx, "missing", domain.KanbanReady); err == nil {
		t.Fatal("a missing session moved")
	}
	rec := st.sessions["mer-1"]
	rec.IsTerminated = true
	st.sessions["mer-1"] = rec
	_, err := svc.BoardMove(ctx, "mer-1", domain.KanbanReady)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "SESSION_TERMINATED" {
		t.Fatalf("terminated: err = %v, want SESSION_TERMINATED", err)
	}
}

func TestBoardMoveReportsAFailedReviewTrigger(t *testing.T) {
	svc, st, _, reviewer := newBoardService(boardStates["PR needs_review"]())
	reviewer.err = errors.New("no reviewer harness")
	_, err := svc.BoardMove(context.Background(), "mer-1", domain.KanbanValidating)
	if refusalReason(err) != "review_trigger_failed" || !strings.Contains(err.Error(), "no reviewer harness") {
		t.Fatalf("err = %v", err)
	}
	if r := st.sessions["mer-1"]; !r.AutoInjectReview || !r.AutoInjectCI {
		t.Fatal("the inject flags were not left on (R14)")
	}
}
