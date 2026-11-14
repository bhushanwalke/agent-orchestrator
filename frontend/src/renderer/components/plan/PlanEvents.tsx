import { InspectorSection } from "@aoagents/product-ui";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { MessageKey } from "../../i18n";
import { formatRelativeTime } from "../../lib/usage-format";
import type { MilestoneStatus, PlanEvent, PlanMilestone } from "../../types/plan";
import { Button } from "../ui/button";
import { StatusGlyph, statusTone } from "./PlanGraph";

const RECENT = 20;

/** Events that read as a milestone state take that state's glyph. */
const eventStatus: Record<string, MilestoneStatus> = {
	milestone_started: "running",
	milestone_done: "done",
	qa_passed: "done",
	plan_done: "done",
	milestone_blocked: "blocked",
	milestone_failed: "failed",
	qa_failed: "failed",
	plan_cancelled: "cancelled",
};

const knownKinds = new Set([
	"plan_created", "kickoff_sent", "kickoff_deferred", "spec_set", "milestone_added", "milestone_updated", "milestone_removed",
	"plan_activated", "milestone_ready", "milestone_started", "milestone_checkpoint", "milestone_blocked", "milestone_done",
	"milestone_failed", "milestone_branch_unresolved", "qa_verdict_requested", "qa_passed", "qa_failed", "defect_sent",
	"plan_done", "plan_done_notified", "plan_cancelled", "sparse_disabled",
]);

/** The Task's latest events, newest first (the daemon's order). */
export function PlanEvents({ events, milestones }: { events: readonly PlanEvent[]; milestones: readonly PlanMilestone[] }) {
	const { t, i18n } = useTranslation();
	const [all, setAll] = useState(false);
	const titles = new Map(milestones.map((m) => [m.key, m.title]));
	const shown = all ? events : events.slice(0, RECENT);
	const now = new Date();

	const describe = (event: PlanEvent): string => {
		const title = titles.get(event.milestoneKey) ?? event.milestoneKey;
		if (knownKinds.has(event.kind)) return t(`plan.events.kind.${event.kind}` as MessageKey, { title });
		return t("plan.events.kind.other", { title, kind: event.kind.replaceAll("_", " ") });
	};

	return (
		<InspectorSection surface={false} title={t("plan.events.heading")}>
			{events.length === 0 ? (
				<p className="text-sm text-muted-foreground">{t("plan.events.empty")}</p>
			) : (
				<ol className="flex flex-col" data-testid="plan-events">
					{shown.map((event) => {
						const status = eventStatus[event.kind] ?? "pending";
						return (
							<li className="flex min-h-control-md items-center gap-2 text-sm" data-testid="plan-event" key={event.id} title={event.note || undefined}>
								<svg aria-hidden="true" className={`shrink-0 ${statusTone[status]}`} height={16} viewBox="-9 -9 18 18" width={16}>
									<StatusGlyph status={status} />
								</svg>
								<span className="min-w-0 flex-1 truncate">{describe(event)}</span>
								<time className="shrink-0 text-xs text-muted-foreground" dateTime={event.createdAt}>
									{formatRelativeTime(event.createdAt, now, i18n.language)}
								</time>
							</li>
						);
					})}
				</ol>
			)}
			{!all && events.length > RECENT ? (
				<Button className="-ml-2 self-start" onClick={() => setAll(true)} size="sm" type="button" variant="quiet">
					{t("plan.events.viewAll")}
				</Button>
			) : null}
		</InspectorSection>
	);
}
