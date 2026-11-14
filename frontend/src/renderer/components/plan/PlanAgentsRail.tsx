import { InspectorSection } from "@aoagents/product-ui";
import { useTranslation } from "react-i18next";
import type { WorkspaceSession } from "../../types/workspace";
import type { Plan, PlanMilestone } from "../../types/plan";
import { BoardSessionCardAdapter } from "../SessionsBoardAdapters";

/**
 * The Task's agents: the orchestrator first, then one compact board card per
 * milestone that has a session, in milestone order.
 */
export function PlanAgentsRail({
	milestones,
	onOpenSession,
	plan,
	selectedKey,
	sessions,
}: {
	milestones: readonly PlanMilestone[];
	onOpenSession: (sessionId: string) => void;
	plan: Plan;
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
			return session ? [{ milestone: m, session }] : [];
		});

	return (
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
	);
}
