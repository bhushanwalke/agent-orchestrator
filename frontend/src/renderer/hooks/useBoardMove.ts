import { useCallback, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { isBoardMoveRefusal, type BoardMoveLane, type BoardMoveRefusal, type BoardMoveSubject } from "../lib/board-moves";
import type { MessageKey } from "../i18n";
import { useUiStore } from "../stores/ui-store";
import { automationsQueryKey } from "./useAutomations";
import { planQueryKey, plansQueryKey } from "./usePlans";
import { workspaceQueryKey } from "./useWorkspaceQuery";

/** The translation key of a refusal's toast. */
export function boardRefusalKey(reason: BoardMoveRefusal): MessageKey {
	return `board.move.refused.${reason}` as MessageKey;
}

function subjectKey(subject: BoardMoveSubject): string {
	switch (subject.kind) {
		case "session":
			return subject.session.id;
		case "plan":
			return `plan:${subject.plan.id}`;
		case "automation":
			return `automation:${subject.automation.id}`;
	}
}

function subjectName(subject: BoardMoveSubject): string {
	switch (subject.kind) {
		case "session":
			return subject.session.title;
		case "plan":
			return subject.plan.title;
		case "automation":
			return subject.automation.displayName;
	}
}

/**
 * Sends a board move to the daemon. A move never sets a column here: the card
 * stays put, showing the working spinner, until the daemon's next read model
 * places it. A refusal is a toast in the person's language.
 */
export function useBoardMove() {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [pending, setPending] = useState<ReadonlySet<string>>(() => new Set());

	const refuse = useCallback((reason: BoardMoveRefusal) => {
		useUiStore.getState().showGlobalToast(t(boardRefusalKey(reason)), undefined, { tone: "error", dedupeKey: "board-move" });
	}, [t]);

	const move = useCallback(async (subject: BoardMoveSubject, to: BoardMoveLane) => {
		const key = subjectKey(subject);
		setPending((current) => new Set(current).add(key));
		try {
			const error = await send(subject, to);
			if (error) {
				const details = (error as { details?: { reason?: unknown } }).details;
				if (isBoardMoveRefusal(details?.reason)) refuse(details.reason);
				else useUiStore.getState().showGlobalToast(t("board.move.failed"), apiErrorMessage(error), { tone: "error", dedupeKey: "board-move" });
				return;
			}
			const name = subjectName(subject);
			if (to === "needs_you" && subject.kind !== "automation") {
				useUiStore.getState().showGlobalToast(t("board.move.askedToPause", { name }), undefined, { tone: "success", dedupeKey: "board-move" });
			} else if (subject.kind !== "automation" && isPaused(subject)) {
				useUiStore.getState().showGlobalToast(t("board.move.askedToContinue", { name }), undefined, { tone: "success", dedupeKey: "board-move" });
			}
		} finally {
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
				subject.kind === "plan" ? queryClient.invalidateQueries({ queryKey: plansQueryKey(subject.plan.projectId) }) : undefined,
				subject.kind === "plan" ? queryClient.invalidateQueries({ queryKey: planQueryKey(subject.plan.id) }) : undefined,
				subject.kind === "automation" ? queryClient.invalidateQueries({ queryKey: automationsQueryKey }) : undefined,
			]);
			setPending((current) => {
				const next = new Set(current);
				next.delete(key);
				return next;
			});
		}
	}, [queryClient, refuse, t]);

	return { move, refuse, isPending: (subject: BoardMoveSubject) => pending.has(subjectKey(subject)) };
}

function isPaused(subject: BoardMoveSubject): boolean {
	if (subject.kind === "session") return subject.session.displayStatus === "Paused for you";
	if (subject.kind === "plan") return subject.plan.displayStatus === "Paused for you" || subject.plan.displayStatus === "On hold";
	return false;
}

async function send(subject: BoardMoveSubject, to: BoardMoveLane): Promise<unknown> {
	switch (subject.kind) {
		case "session": {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/board-move", {
				params: { path: { sessionId: subject.session.id } },
				body: { to },
			});
			return error;
		}
		case "plan": {
			const { error } = await apiClient.POST("/api/v1/plans/{planId}/board-move", {
				params: { path: { planId: subject.plan.id } },
				body: { to },
			});
			return error;
		}
		case "automation": {
			const { error } = await apiClient.POST("/api/v1/automations/{automationId}/run-now", {
				params: { path: { automationId: subject.automation.id } },
			});
			return error;
		}
	}
}
