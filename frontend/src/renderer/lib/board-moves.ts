import type { KanbanColumn } from "@aoagents/product-ui";
import type { WorkspaceSession } from "../types/workspace";
import type { PlanSummary } from "../types/plan";
import type { Automation } from "../hooks/useAutomations";

/** The lanes a card can be dropped on, in board order. Archive is not one. */
export const BOARD_MOVE_LANES = ["queued", "building", "validating", "needs_review", "needs_you", "ready"] as const satisfies readonly KanbanColumn[];
export type BoardMoveLane = (typeof BOARD_MOVE_LANES)[number];

/** Why a move cannot change any fact; each has a `board.move.refused.*` message. */
export const BOARD_MOVE_REFUSALS = [
	"session_cannot_queue",
	"already_there",
	"already_building",
	"cannot_restart",
	"no_pr_provider",
	"no_pr",
	"pr_decides_ready",
	"plan_cannot_validate",
	"plan_decides_ready",
	"plan_not_live",
	"automation_only_runs",
	"pr_is_draft",
] as const;
export type BoardMoveRefusal = (typeof BOARD_MOVE_REFUSALS)[number];

export function isBoardMoveRefusal(value: unknown): value is BoardMoveRefusal {
	return typeof value === "string" && (BOARD_MOVE_REFUSALS as readonly string[]).includes(value);
}

export function isBoardMoveLane(value: unknown): value is BoardMoveLane {
	return typeof value === "string" && (BOARD_MOVE_LANES as readonly string[]).includes(value);
}

/** What a board card stands for, as a move needs it. */
export type BoardMoveSubject =
	| { kind: "session"; session: WorkspaceSession }
	| { kind: "plan"; plan: PlanSummary }
	| { kind: "automation"; automation: Automation };

/**
 * Predicts the daemon's refusal of a move from what the card already knows,
 * so a drag can dim the lanes that would refuse it and the menu can disable
 * them. It never allows what the daemon would refuse on facts the card
 * carries; facts it cannot see (whether the project has a PR provider) are
 * left to the daemon, whose refusal arrives as a toast.
 */
export function predictBoardMoveRefusal(subject: BoardMoveSubject, to: BoardMoveLane): BoardMoveRefusal | null {
	switch (subject.kind) {
		case "automation":
			return to === "building" ? null : "automation_only_runs";
		case "plan":
			return predictPlanRefusal(subject.plan, to);
		case "session":
			return predictSessionRefusal(subject.session, to);
	}
}

function predictSessionRefusal(session: WorkspaceSession, to: BoardMoveLane): BoardMoveRefusal | null {
	const column = session.kanbanColumn ?? "building";
	const stage = session.kanbanStage ?? column;
	const paused = column === "needs_you" && session.displayStatus === "Paused for you";
	if (paused && (to === stage || to === "building")) return null;
	const prs = session.prs ?? [];
	const live = prs.filter((pr) => pr.state === "open" || pr.state === "draft");
	switch (to) {
		case "queued":
			return "session_cannot_queue";
		case "building":
			return stage === "building" ? "already_building" : "cannot_restart";
		case "validating":
			if (stage === "validating" && !paused) return "already_there";
			if (live.length === 0 && prs.length > 0) return "pr_decides_ready";
			return null;
		case "needs_review":
			if (live.length === 0) return "no_pr";
			if (live.every((pr) => pr.state === "draft")) return "pr_is_draft";
			return stage === "needs_review" ? "already_there" : null;
		case "ready":
			if (prs.length > 0) return "pr_decides_ready";
			return column === "ready" ? "already_there" : null;
		case "needs_you":
			return paused ? "already_there" : null;
	}
}

function predictPlanRefusal(plan: PlanSummary, to: BoardMoveLane): BoardMoveRefusal | null {
	const column = plan.kanbanColumn;
	const stage = plan.kanbanStage ?? column;
	const held = plan.displayStatus === "On hold" || plan.displayStatus === "Paused for you";
	if (plan.phase === "done" || plan.phase === "cancelled" || plan.phase === "direct") return "plan_not_live";
	if (held && to !== "queued" && to !== "needs_you" && (to === stage || to === "building")) return null;
	switch (to) {
		case "queued":
			if (plan.displayStatus === "On hold") return "already_there";
			if (plan.phase === "triage" || plan.phase === "planning") return null;
			return plan.phase === "implementing" && plan.milestoneCounts.running === 0 ? null : "plan_not_live";
		case "building":
			return column === "needs_you" ? "cannot_restart" : "already_there";
		case "validating":
		case "needs_review":
			return "plan_cannot_validate";
		case "ready":
			return "plan_decides_ready";
		case "needs_you":
			return plan.displayStatus === "Paused for you" ? "already_there" : null;
	}
}
