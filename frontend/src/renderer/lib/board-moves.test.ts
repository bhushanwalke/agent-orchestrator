import { describe, expect, it } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import type { PlanSummary } from "../types/plan";
import type { Automation } from "../hooks/useAutomations";
import { BOARD_MOVE_REFUSALS, predictBoardMoveRefusal } from "./board-moves";
import { laneKeyboardCoordinates } from "../components/BoardMoves";
import de from "../i18n/de.json";
import en from "../i18n/en.json";
import es from "../i18n/es.json";
import fr from "../i18n/fr.json";
import ja from "../i18n/ja.json";
import ko from "../i18n/ko.json";
import ptBR from "../i18n/pt-BR.json";
import ru from "../i18n/ru.json";
import zhCN from "../i18n/zh-CN.json";

function session(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "s1", title: "work", workspaceId: "p1", workspaceName: "p", provider: "codex", status: "idle",
		kanbanColumn: "building", kanbanStage: "building", displayStatus: "Idle", updatedAt: "", prs: [],
		...overrides,
	} as WorkspaceSession;
}

const pr = (state: "open" | "draft" | "merged" | "closed") => ({ url: `u-${state}`, number: 1, state }) as WorkspaceSession["prs"][number];

function plan(overrides: Partial<PlanSummary>): PlanSummary {
	return {
		id: "plan_1", projectId: "p1", title: "Task", phase: "planning", kanbanColumn: "building", kanbanStage: "building",
		displayStatus: "Planning", milestoneCounts: { total: 0, done: 0, running: 0, failed: 0, blocked: 0 },
		...overrides,
	} as PlanSummary;
}

describe("predictBoardMoveRefusal", () => {
	it("mirrors the daemon's session rules on facts the card carries", () => {
		const s = { kind: "session" as const, session: session({}) };
		expect(predictBoardMoveRefusal(s, "queued")).toBe("session_cannot_queue");
		expect(predictBoardMoveRefusal(s, "building")).toBe("already_building");
		expect(predictBoardMoveRefusal(s, "needs_review")).toBe("no_pr");
		expect(predictBoardMoveRefusal(s, "ready")).toBeNull();
		expect(predictBoardMoveRefusal(s, "needs_you")).toBeNull();
		// Unknown provider: the daemon judges.
		expect(predictBoardMoveRefusal(s, "validating")).toBeNull();
		expect(predictBoardMoveRefusal({ kind: "session", session: session({ hasPrProvider: false }) }, "validating")).toBe("no_pr_provider");
		expect(predictBoardMoveRefusal({ kind: "session", session: session({ hasPrProvider: true }) }, "validating")).toBeNull();

		expect(predictBoardMoveRefusal({ kind: "session", session: session({ prs: [pr("draft")] }) }, "needs_review")).toBe("pr_is_draft");
		expect(predictBoardMoveRefusal({ kind: "session", session: session({ prs: [pr("merged")], kanbanColumn: "ready", kanbanStage: "ready" }) }, "validating")).toBe("pr_decides_ready");
		expect(predictBoardMoveRefusal({ kind: "session", session: session({ prs: [pr("open")] }) }, "ready")).toBe("pr_decides_ready");
	});

	it("lets accepted work go back to its facts from Building", () => {
		const accepted = { kind: "session" as const, session: session({ kanbanColumn: "ready", kanbanStage: "building", displayStatus: "Accepted" }) };
		expect(predictBoardMoveRefusal(accepted, "building")).toBeNull();
		expect(predictBoardMoveRefusal(accepted, "ready")).toBe("already_there");
	});

	it("lets a paused card go back to work", () => {
		const paused = { kind: "session" as const, session: session({ kanbanColumn: "needs_you", kanbanStage: "validating", displayStatus: "Paused for you", prs: [pr("open")] }) };
		expect(predictBoardMoveRefusal(paused, "building")).toBeNull();
		expect(predictBoardMoveRefusal(paused, "validating")).toBeNull();
		expect(predictBoardMoveRefusal(paused, "needs_you")).toBe("already_there");
	});

	it("leaves Validating and Ready to QA for a Task, and holds only what is not running", () => {
		const p = { kind: "plan" as const, plan: plan({}) };
		expect(predictBoardMoveRefusal(p, "validating")).toBe("plan_cannot_validate");
		expect(predictBoardMoveRefusal(p, "needs_review")).toBe("plan_cannot_validate");
		expect(predictBoardMoveRefusal(p, "ready")).toBe("plan_decides_ready");
		expect(predictBoardMoveRefusal(p, "queued")).toBeNull();
		const running = { kind: "plan" as const, plan: plan({ phase: "implementing", milestoneCounts: { total: 2, done: 0, running: 1, failed: 0, blocked: 0 } }) };
		expect(predictBoardMoveRefusal(running, "queued")).toBe("plan_not_live");
		const held = { kind: "plan" as const, plan: plan({ kanbanColumn: "queued", kanbanStage: "queued", displayStatus: "On hold" }) };
		expect(predictBoardMoveRefusal(held, "building")).toBeNull();
		expect(predictBoardMoveRefusal({ kind: "plan", plan: plan({ kanbanColumn: "needs_you", kanbanStage: "building", displayStatus: "Blocked", phase: "implementing" }) }, "building")).toBe("cannot_restart");
	});

	it("only runs an automation", () => {
		const a = { kind: "automation" as const, automation: { id: "a" } as Automation };
		expect(predictBoardMoveRefusal(a, "building")).toBeNull();
		for (const lane of ["queued", "validating", "needs_review", "needs_you", "ready"] as const) {
			expect(predictBoardMoveRefusal(a, lane)).toBe("automation_only_runs");
		}
	});
});

describe("laneKeyboardCoordinates", () => {
	const rect = (left: number) => ({ left, top: 0, width: 100, height: 400, right: left + 100, bottom: 400 });
	const context = { droppableRects: new Map([["queued", rect(0)], ["building", rect(100)], ["validating", rect(200)]]) };
	const step = (code: string, x: number) =>
		laneKeyboardCoordinates({ code, preventDefault: () => {} } as KeyboardEvent, { context, currentCoordinates: { x, y: 50 } } as never);

	it("steps to the centre of the neighbouring lane and stops at the ends", () => {
		expect(step("ArrowRight", 150)).toEqual({ x: 250, y: 50 });
		expect(step("ArrowLeft", 150)).toEqual({ x: 50, y: 50 });
		expect(step("ArrowRight", 250)).toEqual({ x: 250, y: 50 });
		expect(step("ArrowDown", 150)).toBeUndefined();
	});
});

describe("board move translations", () => {
	const locales = { de, es, fr, ja, ko, "pt-BR": ptBR, ru, "zh-CN": zhCN } as unknown as Record<string, Record<string, string>>;
	const english = en as unknown as Record<string, string>;
	const keys = [
		"column.queued", "column.needs_you",
		"displayStatus.starting", "displayStatus.onHold", "displayStatus.scheduled", "displayStatus.pausedForYou",
		"displayStatus.needsADecision", "displayStatus.stuck", "displayStatus.accepted",
		"board.move.moveTo", "board.move.runNow", "board.move.askedToPause", "board.move.askedToContinue",
		"board.lane.emptyQueued", "board.lane.emptyNeedsYou",
		...BOARD_MOVE_REFUSALS.map((reason) => `board.move.refused.${reason}`),
	];

	it("translates every board key in all nine locales", () => {
		for (const key of keys) {
			expect(english[key], key).toBeTruthy();
			for (const [locale, messages] of Object.entries(locales)) {
				expect(messages[key], `${locale} ${key}`).toBeTruthy();
				expect(messages[key], `${locale} ${key} is still English`).not.toBe(english[key]);
			}
		}
	});
});
