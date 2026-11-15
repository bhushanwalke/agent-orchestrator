import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft, ChevronDown, ExternalLink } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { isAgentActivityWorking } from "@aoagents/product-ui";
import { usePlanMutations, usePlanQuery } from "../../hooks/usePlans";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { apiErrorMessage } from "../../lib/api-client";
import { sessionNavigateTarget } from "../../lib/navigate-to-session";
import { formatRelativeTime } from "../../lib/usage-format";
import { cn } from "../../lib/utils";
import type { PlanMilestone, PlanPhase } from "../../types/plan";
import { ChatMarkdown } from "../chat/ChatMarkdown";
import { ConfirmDialog } from "../ConfirmDialog";
import { PageTopbar, PageTopbarTitle } from "../PageTopbar";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "../ui/sheet";
import { CancelPlanDialog } from "./CancelPlanDialog";
import { PlanAgentsRail } from "./PlanAgentsRail";
import { PlanEvents } from "./PlanEvents";
import { PlanGraph } from "./PlanGraph";

/** One semantic tone per phase, from the board's status tokens. */
const phaseDot: Record<PlanPhase, string> = {
	triage: "bg-passive",
	planning: "bg-passive",
	implementing: "bg-status-working",
	qa: "bg-status-validating",
	fixing: "bg-status-needs-you",
	done: "bg-status-ready",
	cancelled: "bg-status-terminated",
	failed: "bg-destructive",
	direct: "bg-status-ready",
};

function isLink(ref: string): boolean {
	return /^https?:\/\//i.test(ref);
}

/** A Task's run view: the milestone graph with its agents and recent events. */
export function PlanRunView({ projectId, planId }: { projectId: string; planId: string }) {
	const { t, i18n } = useTranslation();
	const navigate = useNavigate();
	const query = usePlanQuery(planId);
	const workspaces = useWorkspaceQuery().data ?? [];
	const sessions = workspaces.find((workspace) => workspace.id === projectId)?.sessions ?? [];
	const { retryMilestone, triage } = usePlanMutations(planId, projectId);
	const [selectedKey, setSelectedKey] = useState<string>();
	const [cancelOpen, setCancelOpen] = useState(false);
	const [directOpen, setDirectOpen] = useState(false);
	const [specOpen, setSpecOpen] = useState(false);

	const backToBoard = () => void navigate({ to: "/projects/$projectId", params: { projectId } });
	const openSession = (sessionId: string) => void navigate(sessionNavigateTarget(projectId, sessionId));
	const openMilestone = (m: PlanMilestone) => {
		if (m.session) openSession(m.session.id);
	};

	const detail = query.data;
	const backButton = (
		<Button aria-label={t("plan.run.back")} onClick={backToBoard} size="icon-sm" type="button" variant="quiet">
			<ArrowLeft className="size-icon-sm" aria-hidden="true" />
		</Button>
	);

	if (!detail) {
		return (
			<div className="flex min-h-0 flex-1 flex-col bg-background">
				<PageTopbar lead={<>{backButton}<PageTopbarTitle as="h1">{t("plan.run.loadingTitle")}</PageTopbarTitle></>} />
				{query.isError ? (
					<p className="px-(--space-4_5) py-6 text-sm text-muted-foreground" role="alert">
						{t("plan.run.loadFailed", { message: apiErrorMessage(query.error) })}
					</p>
				) : null}
			</div>
		);
	}

	const { plan, milestones, events } = detail;
	// Agents whose turn is running now; an idle worker that finished is not active.
	const activeAgents = plan.liveSessions.filter((live) => isAgentActivityWorking(sessions.find((session) => session.id === live.id)?.activity)).length;
	const started = plan.activatedAt ?? plan.createdAt;
	const canRerunQA = plan.phase === "done" || plan.phase === "fixing";
	const finished = plan.phase === "cancelled" || plan.phase === "done";
	const deciding = plan.phase === "triage";
	// A direct Task is closed: its worker's own card and session carry the work.
	const direct = plan.phase === "direct";
	const hasSpec = Boolean(plan.specMarkdown.trim());

	const lead = (
		<>
			{backButton}
			<PageTopbarTitle as="h1">{plan.title}</PageTopbarTitle>
			<Badge data-testid="plan-phase" variant="neutral">
				<span aria-hidden="true" className={cn("size-1.5 rounded-full", phaseDot[plan.phase])} />
				{t(`plan.phase.${plan.phase}`)}
			</Badge>
			<span className="hidden min-w-0 truncate text-sm text-muted-foreground md:inline">
				{t("plan.run.started", { time: formatRelativeTime(started, new Date(), i18n.language), count: activeAgents })}
			</span>
			{plan.ticketRef ? (
				isLink(plan.ticketRef) ? (
					<a
						className="hidden min-w-0 items-center gap-1 truncate text-sm text-muted-foreground hover:text-foreground md:inline-flex"
						href={plan.ticketRef}
						rel="noreferrer"
						target="_blank"
					>
						{t("plan.run.ticket")}
						<ExternalLink className="size-icon-xs" aria-hidden="true" />
					</a>
				) : (
					<span className="hidden truncate font-mono text-sm text-muted-foreground md:inline">{plan.ticketRef}</span>
				)
			) : null}
		</>
	);

	const actions = (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button size="sm" type="button" variant="secondary">
					{t("plan.actions.label")}
					<ChevronDown className="size-icon-sm" aria-hidden="true" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end">
				<DropdownMenuItem disabled={!plan.orchestratorSessionId} onSelect={() => openSession(plan.orchestratorSessionId)}>
					{t("plan.actions.openOrchestrator")}
				</DropdownMenuItem>
				{deciding ? (
					<>
						<DropdownMenuItem disabled={triage.isPending} onSelect={() => triage.mutate("plan")}>
							{t("plan.actions.planIt")}
						</DropdownMenuItem>
						<DropdownMenuItem disabled={triage.isPending} onSelect={() => setDirectOpen(true)}>
							{t("plan.actions.sendToWorker")}
						</DropdownMenuItem>
					</>
				) : null}
				{deciding || direct ? null : (
					<DropdownMenuItem disabled={!canRerunQA || retryMilestone.isPending} onSelect={() => retryMilestone.mutate("qa")}>
						{t("plan.actions.rerunQa")}
					</DropdownMenuItem>
				)}
				{direct && !hasSpec ? null : (
					<DropdownMenuItem disabled={!hasSpec} onSelect={() => setSpecOpen(true)}>
						{t("plan.actions.showSpec")}
					</DropdownMenuItem>
				)}
				{direct ? null : (
					<>
						<DropdownMenuSeparator />
						<DropdownMenuItem disabled={finished} onSelect={() => setCancelOpen(true)} variant="destructive">
							{t("plan.actions.cancel")}
						</DropdownMenuItem>
					</>
				)}
			</DropdownMenuContent>
		</DropdownMenu>
	);

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<PageTopbar actions={actions} lead={lead} />
			{retryMilestone.isError ? (
				<p className="px-(--space-4_5) pt-3 text-sm text-destructive" role="alert">
					{t("plan.actions.rerunFailed", { message: apiErrorMessage(retryMilestone.error) })}
				</p>
			) : null}
			{triage.isError && !directOpen ? (
				<p className="px-(--space-4_5) pt-3 text-sm text-destructive" role="alert">
					{t("plan.triage.failed", { message: apiErrorMessage(triage.error) })}
				</p>
			) : null}
			<div className="flex min-h-0 flex-1 flex-col overflow-y-auto min-[900px]:flex-row min-[900px]:overflow-hidden">
				<div className="min-w-0 flex-1 px-(--space-4_5) py-6 min-[900px]:overflow-auto">
					{deciding ? (
						<p className="py-12 text-center text-sm text-muted-foreground">{t("plan.triage.waiting")}</p>
					) : direct ? (
						<div className="flex flex-col items-center gap-2 py-12 text-center">
							<p className="text-sm font-medium text-foreground">{t("plan.direct.heading")}</p>
							{plan.triage?.reason ? <p className="max-w-prose text-sm text-muted-foreground">{plan.triage.reason}</p> : null}
							{plan.directSessionId ? (
								<Button className="mt-2" onClick={() => openSession(plan.directSessionId ?? "")} size="sm" type="button" variant="primary">
									{t("plan.direct.open")}
								</Button>
							) : null}
						</div>
					) : milestones.length === 0 ? (
						<p className="text-sm text-muted-foreground">{t("plan.graph.empty")}</p>
					) : (
						<PlanGraph milestones={milestones} onOpen={openMilestone} onSelect={setSelectedKey} selectedKey={selectedKey} />
					)}
				</div>
				<aside
					aria-label={t("plan.run.rail")}
					className="flex shrink-0 flex-col border-t border-border py-3 min-[900px]:w-80 min-[900px]:overflow-y-auto min-[900px]:border-t-0 min-[900px]:border-l"
				>
					<PlanAgentsRail
						milestones={milestones}
						onOpenSession={openSession}
						plan={plan}
						research={detail.researchSessions}
						selectedKey={selectedKey}
						sessions={sessions}
					/>
					<PlanEvents events={events} milestones={milestones} triage={plan.triage} />
				</aside>
			</div>
			<CancelPlanDialog onOpenChange={setCancelOpen} open={cancelOpen} planId={planId} projectId={projectId} title={plan.title} />
			<ConfirmDialog
				open={directOpen}
				onOpenChange={(next) => {
					if (!next) triage.reset();
					setDirectOpen(next);
				}}
				title={t("plan.triage.directTitle", { title: plan.title })}
				description={t("plan.triage.directDescription")}
				confirmLabel={t("plan.actions.sendToWorker")}
				cancelLabel={t("plan.triage.keepDeciding")}
				busy={triage.isPending}
				error={triage.isError ? apiErrorMessage(triage.error, t("plan.triage.directFailed")) : null}
				onConfirm={() => triage.mutate("direct", { onSuccess: () => setDirectOpen(false) })}
			/>
			<Sheet onOpenChange={setSpecOpen} open={specOpen}>
				<SheetContent className="w-full overflow-y-auto sm:max-w-2xl" side="right">
					<SheetHeader>
						<SheetTitle>{t("plan.spec.title")}</SheetTitle>
						<SheetDescription>{plan.title}</SheetDescription>
					</SheetHeader>
					<div className="px-4 pb-6">
						<ChatMarkdown text={plan.specMarkdown} />
					</div>
				</SheetContent>
			</Sheet>
		</div>
	);
}
