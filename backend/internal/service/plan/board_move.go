package plan

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// EventBoardMove records a board move on a Task: a hold, an attention
// request, or a release, with the lane in the note.
const EventBoardMove = "board_move"

// EventTriageDeferred records a direct triage decision made while the Task
// was held; the worker starts when the hold is released.
const EventTriageDeferred = "triage_deferred"

// held reports a plan the person put on hold or asked to pause from the
// board. While held the scheduler spawns no milestone worker, runs no direct
// spawn of a triage decision, retries no kickoff and sends no triage
// reminder. Running milestones and the QA loop continue.
func held(p domain.Plan) bool { return p.BoardRequest != domain.BoardRequestNone }

// BoardMove applies a person's drop of a Task card on a board lane. Like a
// session move it never sets a column: it records a hold or an attention
// request, or releases one, and the card lands where the reducer puts it. A
// move that cannot change anything is refused before any side effect.
func (s *Service) BoardMove(ctx context.Context, id domain.PlanID, to domain.KanbanColumn) (Detail, error) {
	if !domain.BoardMoveTarget(to) {
		return Detail{}, apierr.Invalid("INVALID_BOARD_COLUMN", "Unknown board lane", map[string]any{"field": "to"})
	}
	d, err := s.Get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	switch d.Phase {
	case domain.PlanPhaseCancelled, domain.PlanPhaseDone, domain.PlanPhaseDirect:
		return Detail{}, apierr.Conflict("PLAN_NOT_LIVE", "This Task is finished; its card cannot be moved", map[string]any{"reason": string(domain.BoardRefusalPlanNotLive)})
	}
	move, refusal := planBoardMove(d, to)
	if refusal != "" {
		return Detail{}, apierr.Conflict("BOARD_MOVE_UNSUPPORTED", planRefusalMessage(refusal), map[string]any{"reason": string(refusal)})
	}
	p := d.Plan
	if move.release {
		if err := s.setBoardRequest(ctx, p, domain.BoardRequestNone, to); err != nil {
			return Detail{}, err
		}
		s.sendOrchestrator(ctx, p, planResumeMessage(p))
		s.releaseHeld(ctx, p.ID)
	}
	if move.request != domain.BoardRequestNone {
		if err := s.setBoardRequest(ctx, p, move.request, to); err != nil {
			return Detail{}, err
		}
		s.sendOrchestrator(ctx, p, planHoldMessage(p, move.request == domain.BoardRequestAttention))
	}
	return s.Get(ctx, id)
}

type planMove struct {
	release bool
	request domain.BoardRequest
}

func planBoardMove(d Detail, to domain.KanbanColumn) (planMove, domain.BoardMoveRefusal) {
	p, column, stage := d.Plan, d.Kanban, d.KanbanStage
	if stage == "" {
		stage = column
	}
	release := held(p) && to != domain.KanbanQueued && to != domain.KanbanNeedsYou
	// Released back to its own lane, or to Building, which is where a held
	// Task goes to start again: the release is the whole move.
	if release && (to == stage || to == domain.KanbanBuilding) {
		return planMove{release: true}, ""
	}
	switch to {
	case domain.KanbanQueued:
		if p.BoardRequest == domain.BoardRequestHold {
			return planMove{}, domain.BoardRefusalAlreadyThere
		}
		if !holdable(d) {
			return planMove{}, domain.BoardRefusalPlanNotLive
		}
		return planMove{request: domain.BoardRequestHold}, ""
	case domain.KanbanBuilding:
		if column == domain.KanbanNeedsYou {
			return planMove{}, domain.BoardRefusalCannotRestart
		}
		return planMove{}, domain.BoardRefusalAlreadyThere
	case domain.KanbanValidating, domain.KanbanNeedsReview:
		return planMove{}, domain.BoardRefusalPlanCannotValidate
	case domain.KanbanReady:
		return planMove{}, domain.BoardRefusalPlanDecidesReady
	case domain.KanbanNeedsYou:
		if p.BoardRequest == domain.BoardRequestAttention {
			return planMove{}, domain.BoardRefusalAlreadyThere
		}
		return planMove{request: domain.BoardRequestAttention}, ""
	}
	return planMove{}, domain.BoardRefusalAlreadyThere
}

// holdable reports a Task that can be put on hold without stopping work in
// flight: still triaging or planning, or implementing with no milestone
// running.
func holdable(d Detail) bool {
	switch d.Phase {
	case domain.PlanPhaseTriage, domain.PlanPhasePlanning:
		return true
	case domain.PlanPhaseImplementing:
		for _, v := range d.Milestones {
			if v.Milestone.Status == domain.MilestoneStatusRunning {
				return false
			}
		}
		return true
	}
	return false
}

func (s *Service) setBoardRequest(ctx context.Context, p domain.Plan, request domain.BoardRequest, lane domain.KanbanColumn) error {
	s.mu.Lock()
	ok, err := s.store.SetPlanBoardRequest(ctx, p.ID, request, s.now().UTC())
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("PLAN_NOT_FOUND", "Unknown plan")
	}
	note := string(lane)
	if request != domain.BoardRequestNone {
		note = string(request) + " → " + note
	} else {
		note = "released → " + note
	}
	s.recordEvent(ctx, p.ID, "", "", EventBoardMove, note)
	return nil
}

// releaseHeld lets the work a hold deferred go ahead: a direct triage
// decision starts its worker, a kickoff not yet delivered is retried, and the
// scheduler ticks now so ready milestones spawn.
func (s *Service) releaseHeld(ctx context.Context, id domain.PlanID) {
	if p, err := s.requirePlan(ctx, id); err == nil && p.State == domain.PlanStateTriage &&
		p.Triage != nil && p.Triage.Decision == domain.TriageDecisionDirect {
		if _, err := s.runDeferredDirect(ctx, p); err != nil {
			s.logger.Warn("deferred direct spawn failed", "plan", id, "error", err)
		}
	}
	if _, err := s.RetryDeferredKickoff(ctx, id); err != nil {
		s.logger.Warn("kickoff after release failed", "plan", id, "error", err)
	}
	s.Wake()
}

func (s *Service) sendOrchestrator(ctx context.Context, p domain.Plan, message string) {
	if p.OrchestratorSessionID == "" {
		return
	}
	if err := s.sessions.Send(ctx, p.OrchestratorSessionID, message, ports.MessageDeliveryOptions{}); err != nil {
		s.logger.Info("board notice to orchestrator refused", "plan", p.ID, "error", err)
	}
}

func planHoldMessage(p domain.Plan, summarise bool) string {
	ask := ""
	if summarise {
		ask = " Then reply to the person in two to five lines: what is done, what is in progress, what you need from them."
	}
	return fmt.Sprintf(`<ao-plan-hold>
The person put Task %s %q on hold from the board. Do not spawn or send to workers for this Task until an <ao-plan-resume> notice arrives. Running milestones keep going.%s
</ao-plan-hold>`, p.ID, p.Title, ask)
}

func planResumeMessage(p domain.Plan) string {
	return fmt.Sprintf(`<ao-plan-resume>
Task %s %q is released. Continue.
</ao-plan-resume>`, p.ID, p.Title)
}

var planRefusalMessages = map[domain.BoardMoveRefusal]string{
	domain.BoardRefusalAlreadyThere:       "The card is already in that lane.",
	domain.BoardRefusalCannotRestart:      "This Task needs a decision from you first; open it to see why.",
	domain.BoardRefusalPlanCannotValidate: "A Task's QA starts by itself when its last milestone is done.",
	domain.BoardRefusalPlanDecidesReady:   "A Task is ready when its QA passes.",
	domain.BoardRefusalPlanNotLive:        "This Task cannot be put on hold while a milestone is running.",
}

func planRefusalMessage(reason domain.BoardMoveRefusal) string {
	if msg, ok := planRefusalMessages[reason]; ok {
		return msg
	}
	return string(reason)
}
