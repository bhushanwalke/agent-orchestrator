import { useTranslation } from "react-i18next";
import { ListTodo } from "../app-icons";
import { toAgentProvider } from "../../types/workspace";
import type { PlanSessionSummary } from "../../types/plan";
import { AgentAvatar } from "../AgentAvatar";

const SHOWN = 4;

/** A Task's live agents as small overlapping marks, with a "+n" for the rest. */
export function PlanAvatarStrip({ sessions }: { sessions: readonly PlanSessionSummary[] }) {
	const { t } = useTranslation();
	if (sessions.length === 0) {
		return (
			<span className="inline-flex size-icon-xl shrink-0 items-center justify-center text-muted-foreground" aria-label={t("board.plans.noAgents")} role="img">
				<ListTodo className="size-icon-md" aria-hidden />
			</span>
		);
	}
	const shown = sessions.slice(0, SHOWN);
	const rest = sessions.length - shown.length;
	return (
		<span className="inline-flex shrink-0 items-center" aria-label={t("board.plans.agents", { n: sessions.length })} role="group">
			<span className="inline-flex items-center -space-x-1.5">
				{shown.map((session) => (
					<span key={session.id} data-testid="plan-agent-avatar" className="inline-flex rounded-full ring-2 ring-surface">
						<AgentAvatar provider={toAgentProvider(session.harness)} />
					</span>
				))}
			</span>
			{rest > 0 ? <span className="ml-1 text-2xs tabular-nums text-muted-foreground">+{rest}</span> : null}
		</span>
	);
}
