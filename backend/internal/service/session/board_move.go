package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// BoardReviewer is the part of AO's review service a board move drives: a
// drop on Validating asks for a review of the PR, a drop on In review stops
// a running pass. Late-bound because the review service is built after this
// one.
type BoardReviewer interface {
	TriggerBoardReview(ctx context.Context, worker domain.SessionID, prURL string) error
	CancelRunningReview(ctx context.Context, worker domain.SessionID) error
}

// boardRequestWriter is the narrow store write for a session's board request.
type boardRequestWriter interface {
	SetSessionBoardRequest(ctx context.Context, id domain.SessionID, request domain.BoardRequest, at time.Time) (bool, error)
}

// SetBoardReviewer late-binds the review operations board moves use.
func (s *Service) SetBoardReviewer(r BoardReviewer) {
	s.boardReviewer = r
}

// BoardMove applies a person's drop of a session card on a board lane. A
// move never sets a column: it changes a fact the reducer reads or sends the
// agent an instruction, and the card lands where the facts then put it. A
// move that cannot change anything is refused before any side effect, with a
// reason the app translates.
func (s *Service) BoardMove(ctx context.Context, id domain.SessionID, to domain.KanbanColumn) (domain.Session, error) {
	if !domain.BoardMoveTarget(to) {
		return domain.Session{}, apierr.Invalid("INVALID_BOARD_COLUMN", "Unknown board lane", map[string]any{"field": "to"})
	}
	sess, err := s.Get(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	if sess.IsTerminated {
		return domain.Session{}, apierr.Conflict("SESSION_TERMINATED", "Session is terminated", nil)
	}
	writer, ok := s.store.(boardRequestWriter)
	if !ok {
		return domain.Session{}, apierr.NotImplemented("BOARD_MOVE_UNAVAILABLE", "Board moves are unavailable")
	}
	plan := planSessionMove(sess, to, s.prProviderLookup(ctx)(sess.ProjectID))
	if plan.refusal != "" {
		return domain.Session{}, boardMoveRefused(plan.refusal)
	}
	now := s.now()
	if plan.release {
		if _, err := writer.SetSessionBoardRequest(ctx, id, domain.BoardRequestNone, now); err != nil {
			return domain.Session{}, err
		}
		s.sendBoardNotice(ctx, id, boardResumeNotice(to))
		if plan.releaseOnly {
			return s.Get(ctx, id)
		}
	}
	switch to {
	case domain.KanbanValidating:
		if len(livePRs(sess.PRs)) == 0 {
			s.sendBoardNotice(ctx, id, boardDeliverNotice(sess.SessionRecord))
			break
		}
		if err := s.handPRToAO(ctx, sess); err != nil {
			return domain.Session{}, err
		}
	case domain.KanbanNeedsReview:
		if err := s.handPRToPerson(ctx, id); err != nil {
			return domain.Session{}, err
		}
	case domain.KanbanReady:
		if _, err := writer.SetSessionBoardRequest(ctx, id, domain.BoardRequestAccepted, now); err != nil {
			return domain.Session{}, err
		}
	case domain.KanbanNeedsYou:
		if _, err := writer.SetSessionBoardRequest(ctx, id, domain.BoardRequestAttention, now); err != nil {
			return domain.Session{}, err
		}
		s.sendBoardNotice(ctx, id, boardHoldNotice)
	}
	return s.Get(ctx, id)
}

// sessionMovePlan is what a drop will do, decided from the read model before
// anything changes.
type sessionMovePlan struct {
	refusal domain.BoardMoveRefusal
	// release clears the person's attention request and tells the agent to
	// continue; releaseOnly means that is the whole move.
	release     bool
	releaseOnly bool
}

func planSessionMove(sess domain.Session, to domain.KanbanColumn, hasPRProvider bool) sessionMovePlan {
	from, stage := sess.KanbanColumn, sess.KanbanStage
	if stage == "" {
		stage = from
	}
	paused := from == domain.KanbanNeedsYou && sess.BoardRequest == domain.BoardRequestAttention
	refuse := func(r domain.BoardMoveRefusal) sessionMovePlan { return sessionMovePlan{refusal: r} }
	// Released back to the lane it is in: "handled, continue". Building is
	// the plain release whatever the stage, since the card goes back to it.
	if paused && (to == stage || to == domain.KanbanBuilding) {
		return sessionMovePlan{release: true, releaseOnly: true}
	}
	live := livePRs(sess.PRs)
	switch to {
	case domain.KanbanQueued:
		return refuse(domain.BoardRefusalSessionCannotQueue)
	case domain.KanbanBuilding:
		if stage == domain.KanbanBuilding {
			return refuse(domain.BoardRefusalAlreadyBuilding)
		}
		return refuse(domain.BoardRefusalCannotRestart)
	case domain.KanbanValidating:
		if stage == domain.KanbanValidating && !paused {
			return refuse(domain.BoardRefusalAlreadyThere)
		}
		if len(live) == 0 && len(sess.PRs) > 0 {
			return refuse(domain.BoardRefusalAlreadyThere)
		}
		if len(live) == 0 && !hasPRProvider {
			return refuse(domain.BoardRefusalNoPRProvider)
		}
	case domain.KanbanNeedsReview:
		if len(live) == 0 {
			return refuse(domain.BoardRefusalNoPR)
		}
		if allDrafts(live) {
			return refuse(domain.BoardRefusalPRIsDraft)
		}
		// Already a person's turn, whether it shows in In review or in Needs
		// you: there is no AO loop left to stop.
		if stage == domain.KanbanNeedsReview {
			return refuse(domain.BoardRefusalAlreadyThere)
		}
	case domain.KanbanReady:
		if len(sess.PRs) > 0 {
			return refuse(domain.BoardRefusalPRDecidesReady)
		}
		if from == domain.KanbanReady {
			return refuse(domain.BoardRefusalAlreadyThere)
		}
	case domain.KanbanNeedsYou:
		if sess.BoardRequest == domain.BoardRequestAttention {
			return refuse(domain.BoardRefusalAlreadyThere)
		}
	}
	return sessionMovePlan{release: paused}
}

func livePRs(prs []domain.PRFacts) []domain.PRFacts {
	var live []domain.PRFacts
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			live = append(live, pr)
		}
	}
	return live
}

func allDrafts(prs []domain.PRFacts) bool {
	for _, pr := range prs {
		if !pr.Draft {
			return false
		}
	}
	return len(prs) > 0
}

// handPRToAO turns AO's loops back on for the session's PRs and asks for a
// review of the newest live one, so AO owns the next step again.
func (s *Service) handPRToAO(ctx context.Context, sess domain.Session) error {
	if _, err := s.SetAutoInjectReview(ctx, sess.ID, true); err != nil {
		return err
	}
	if _, err := s.SetAutoInjectCI(ctx, sess.ID, true); err != nil {
		return err
	}
	if s.boardReviewer == nil {
		return nil
	}
	pr := newestPR(livePRs(sess.PRs))
	if err := s.boardReviewer.TriggerBoardReview(ctx, sess.ID, pr.URL); err != nil {
		return apierr.Conflict("BOARD_MOVE_UNSUPPORTED", "Could not start a review: "+err.Error(), map[string]any{"reason": "review_trigger_failed"})
	}
	return nil
}

// handPRToPerson stops every AO loop the reducer credits, so the PR's next
// turn is a person's.
func (s *Service) handPRToPerson(ctx context.Context, id domain.SessionID) error {
	if _, err := s.SetAutoInjectReview(ctx, id, false); err != nil {
		return err
	}
	if _, err := s.SetAutoInjectCI(ctx, id, false); err != nil {
		return err
	}
	if _, err := s.SetAutoReview(ctx, id, false); err != nil {
		return err
	}
	if s.boardReviewer == nil {
		return nil
	}
	return s.boardReviewer.CancelRunningReview(ctx, id)
}

func newestPR(prs []domain.PRFacts) domain.PRFacts {
	newest := prs[0]
	for _, pr := range prs[1:] {
		if pr.UpdatedAt.After(newest.UpdatedAt) {
			newest = pr
		}
	}
	return newest
}

func boardMoveRefused(reason domain.BoardMoveRefusal) error {
	return apierr.Conflict("BOARD_MOVE_UNSUPPORTED", boardRefusalMessages[reason], map[string]any{"reason": string(reason)})
}

// boardRefusalMessages are the English refusal messages the CLI prints; the
// app shows its own translation of the reason.
var boardRefusalMessages = map[domain.BoardMoveRefusal]string{
	domain.BoardRefusalSessionCannotQueue: "A running session cannot go back to the queue. Move it to Needs you to pause it.",
	domain.BoardRefusalAlreadyThere:       "The card is already in that lane.",
	domain.BoardRefusalAlreadyBuilding:    "The session is already building.",
	domain.BoardRefusalCannotRestart:      "A session cannot go back to Building once it has moved on.",
	domain.BoardRefusalNoPRProvider:       "This project has no pull request provider, so there is nothing to validate. Accept the work from Ready instead.",
	domain.BoardRefusalNoPR:               "The session has no pull request to hand to a person.",
	domain.BoardRefusalPRDecidesReady:     "The pull request decides when this is ready.",
	domain.BoardRefusalPlanCannotValidate: "A Task's QA starts by itself when its last milestone is done.",
	domain.BoardRefusalPlanDecidesReady:   "A Task is ready when its QA passes.",
	domain.BoardRefusalPlanNotLive:        "This Task cannot be put on hold now.",
	domain.BoardRefusalAutomationOnlyRuns: "An automation can only be run now from the queue.",
	domain.BoardRefusalPRIsDraft:          "This pull request is a draft. Mark it ready for review on the provider first.",
}

// BoardRefusalMessage is the English message for a refusal reason.
func BoardRefusalMessage(reason domain.BoardMoveRefusal) string {
	return boardRefusalMessages[reason]
}

// sendBoardNotice delivers a board notice to the agent. The move's fact is
// already recorded, so an agent that cannot be reached right now does not
// undo it; the failure is logged.
func (s *Service) sendBoardNotice(ctx context.Context, id domain.SessionID, notice string) {
	if err := s.Send(ctx, id, notice, nil); err != nil && s.logger != nil {
		s.logger.Warn("board notice not delivered", "session", id, "err", err)
	}
}

const boardHoldNotice = `<ao-board-hold>
The person moved this session to "Needs you" on the board. Finish the step you are in, do not start new work, then reply in two to five lines: what is done, what is in progress, what you need from them. Then wait.
</ao-board-hold>`

func boardResumeNotice(to domain.KanbanColumn) string {
	return fmt.Sprintf(`<ao-board-resume>
The person moved this session back to the board's %q lane. Continue the task from where you stopped.
</ao-board-resume>`, BoardLaneLabel(to))
}

func boardDeliverNotice(rec domain.SessionRecord) string {
	base := "the project's base branch"
	if rec.BranchState != nil && strings.TrimSpace(rec.BranchState.Base) != "" {
		base = rec.BranchState.Base
	}
	return fmt.Sprintf(`<ao-board-deliver>
The person moved this session to "Validating". Push your branch to origin, open a pull request against %s, then run `+"`ao review trigger --pr <url>`"+`. If the push or PR fails, run `+"`ao report --stuck --note <why>`"+`.
</ao-board-deliver>`, base)
}

// BoardLaneLabel is a lane's English name, as notices and the CLI print it.
func BoardLaneLabel(column domain.KanbanColumn) string {
	switch column {
	case domain.KanbanQueued:
		return "In queue"
	case domain.KanbanBuilding:
		return "Building"
	case domain.KanbanValidating:
		return "Validating"
	case domain.KanbanNeedsReview:
		return "In review"
	case domain.KanbanNeedsYou:
		return "Needs you"
	case domain.KanbanReady:
		return "Ready"
	case domain.KanbanArchive:
		return "Archive"
	}
	return string(column)
}
