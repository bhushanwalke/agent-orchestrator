import type { TFunction } from "i18next";
import type { MessageKey } from "../i18n";

// The daemon's Task phrases (domain.DerivePlanKanban), each with its
// translation key. "Implementing d/t" carries its counts.
const PLAN_STATUS_KEYS: Record<string, MessageKey> = {
	Planning: "displayStatus.planning",
	Deciding: "displayStatus.deciding",
	"Fixing QA findings": "displayStatus.fixingQaFindings",
	Testing: "displayStatus.testing",
	"QA passed": "displayStatus.qaPassed",
	"Needs you": "displayStatus.needsYou",
	Blocked: "displayStatus.blocked",
	"On hold": "displayStatus.onHold",
	"Needs a decision": "displayStatus.needsADecision",
	"Paused for you": "displayStatus.pausedForYou",
	Cancelled: "displayStatus.cancelled",
	"One worker": "displayStatus.oneWorker",
};

const IMPLEMENTING = /^Implementing (\d+)\/(\d+)$/;

/**
 * A Task card's status in the person's language. A phrase this build does
 * not know yet (a newer daemon) prints as the daemon sent it.
 */
export function planStatusLabel(displayStatus: string, t: TFunction): string {
	const implementing = IMPLEMENTING.exec(displayStatus);
	if (implementing) {
		return t("displayStatus.implementing" as MessageKey, { done: Number(implementing[1]), total: Number(implementing[2]) });
	}
	const key = PLAN_STATUS_KEYS[displayStatus];
	return key ? t(key) : displayStatus;
}
