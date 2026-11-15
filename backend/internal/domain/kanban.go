package domain

import "github.com/aoagents/agent-orchestrator/backend/pkg/contract"

// KanbanColumn is the derived delivery-lifecycle placement of a session:
// queued, building, the AO-driven validating loop, the review-feedback loop
// (needs_review), needs_you, ready, or archive.
type KanbanColumn = contract.KanbanColumn

// Kanban columns.
const (
	KanbanQueued      = contract.KanbanQueued
	KanbanNeedsYou    = contract.KanbanNeedsYou
	KanbanBuilding    = contract.KanbanBuilding
	KanbanValidating  = contract.KanbanValidating
	KanbanNeedsReview = contract.KanbanNeedsReview
	KanbanReady       = contract.KanbanReady
	KanbanArchive     = contract.KanbanArchive
)

// DisplayStatus is the phrase shown inside a session's Kanban column: what is
// happening right now at the stage the column already placed it in.
type DisplayStatus = contract.DisplayStatus

// KanbanPresentation is a session's derived board presentation: its column plus
// the display status derived inside that column.
type KanbanPresentation = contract.KanbanPresentation

// BoardRequest is the person's durable intent from a manual board move: a
// fact the reducers read, never a stored column.
type BoardRequest = contract.BoardRequest

// Board requests. Hold applies to Tasks, Accepted to sessions, Attention to
// both.
const (
	BoardRequestNone      = contract.BoardRequestNone
	BoardRequestHold      = contract.BoardRequestHold
	BoardRequestAttention = contract.BoardRequestAttention
	BoardRequestAccepted  = contract.BoardRequestAccepted
)

// BoardMoveRefusal says why a board move cannot change any fact, so the card
// stays where it is. Every value has a translated message in the app.
type BoardMoveRefusal string

// Board move refusals.
const (
	BoardRefusalSessionCannotQueue BoardMoveRefusal = "session_cannot_queue"
	BoardRefusalAlreadyThere       BoardMoveRefusal = "already_there"
	BoardRefusalAlreadyBuilding    BoardMoveRefusal = "already_building"
	BoardRefusalCannotRestart      BoardMoveRefusal = "cannot_restart"
	BoardRefusalNoPRProvider       BoardMoveRefusal = "no_pr_provider"
	BoardRefusalNoPR               BoardMoveRefusal = "no_pr"
	BoardRefusalPRDecidesReady     BoardMoveRefusal = "pr_decides_ready"
	BoardRefusalPlanCannotValidate BoardMoveRefusal = "plan_cannot_validate"
	BoardRefusalPlanDecidesReady   BoardMoveRefusal = "plan_decides_ready"
	BoardRefusalPlanNotLive        BoardMoveRefusal = "plan_not_live"
	BoardRefusalAutomationOnlyRuns BoardMoveRefusal = "automation_only_runs"
	BoardRefusalPRIsDraft          BoardMoveRefusal = "pr_is_draft"
)

// BoardMoveTarget reports whether column is a lane a card can be moved to.
// Archive is not: terminating and cancelling stay card-menu actions.
func BoardMoveTarget(column KanbanColumn) bool {
	switch column {
	case KanbanQueued, KanbanBuilding, KanbanValidating, KanbanNeedsReview, KanbanNeedsYou, KanbanReady:
		return true
	}
	return false
}
