import { InspectorSection } from "@aoagents/product-ui";
import { useTranslation } from "react-i18next";
import type { WorkspaceSession } from "../../types/workspace";
import type { Plan, PlanDetail, PlanMilestone } from "../../types/plan";
import { BoardSessionCardAdapter } from "../SessionsBoardAdapters";

/**
 * The Task's agents: the orchestrator first, then one compact board card per
 * milestone that has a session, in milestone order. Research workers attached
 * without a milestone follow in their own section.
 */
export function PlanAgentsRail({
	milestones,
	onOpenSession,
	plan,
	research = [],
	selectedKey,
	sessions,
}: {
	milestones: readonly PlanMilestone[];
	onOpenSession: (sessionId: string) => void;
	plan: Plan;
	/** Sessions attached to the Task without a milestone, oldest first. */
	research?: PlanDetail["researchSessions"];
	selectedKey?: string;
	/** The project's sessions as the board knows them. */
	sessions: readonly WorkspaceSession[];
}) {
	const { t } = useTranslation();
	const byId = new Map(sessions.map((session) => [session.id, session]));
	const orchestrator = plan.orchestratorSessionId ? byId.get(plan.orchestratorSessionId) : undefined;
	const agents = [...milestones]
		.sort((a, b) => a.ordinal - b.ordinal)
		.flatMap((m) => {
			const session = m.session ? byId.get(m.session.id) : undefined;
			// The card names the branch the milestone's work is recorded on.
			return session ? [{ milestone: m, session: m.branch ? { ...session, branch: m.branch } : session }] : [];
		});
	// A killed research worker has left the board, so it leaves the rail too.
	const researchers = research.flatMap((r) => byId.get(r.id) ?? []);

	return (
		<>
			<InspectorSection surface={false} title={t("plan.agents.heading")}>
				{!orchestrator && agents.length === 0 ? <p className="text-sm text-muted-foreground">{t("plan.agents.empty")}</p> : null}
				<ul className="flex flex-col gap-3">
					{orchestrator ? (
						<li className="flex flex-col gap-1" data-testid="plan-agent-card">
							<span className="text-xs text-muted-foreground">{t("plan.agents.orchestrator")}</span>
							<BoardSessionCardAdapter compact onOpen={() => onOpenSession(orchestrator.id)} session={orchestrator} />
						</li>
					) : null}
					{agents.map(({ milestone, session }) => (
						<li className="flex flex-col gap-1" data-milestone={milestone.key} data-testid="plan-agent-card" key={`${milestone.key}:${session.id}`}>
							<span className="truncate text-xs text-muted-foreground">{t("plan.agents.milestone", { key: milestone.key, title: milestone.title })}</span>
							<BoardSessionCardAdapter
								compact
								onOpen={() => onOpenSession(session.id)}
								selected={milestone.key === selectedKey}
								session={session}
							/>
						</li>
					))}
				</ul>
			</InspectorSection>
			{researchers.length > 0 ? (
				<InspectorSection surface={false} title={t("plan.research.heading")}>
					<ul className="flex flex-col gap-3">
						{researchers.map((session) => (
							<li data-testid="plan-research-card" key={session.id}>
								<BoardSessionCardAdapter compact onOpen={() => onOpenSession(session.id)} session={session} />
							</li>
						))}
					</ul>
				</InspectorSection>
			) : null}
		</>
	);
}
