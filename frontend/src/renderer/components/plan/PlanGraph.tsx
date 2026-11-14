import { Check, Minus, Pause, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { agentLabel } from "../../lib/agent-options";
import { formatScriptDuration } from "../../lib/cloud-script-runs";
import { layoutPlan, planNodeOrder } from "../../lib/plan-layout";
import { cn } from "../../lib/utils";
import type { MilestoneStatus, PlanMilestone } from "../../types/plan";

// Node centers sit inside this padding; labels hang below each node.
const PAD_X = 100;
const PAD_Y = 28;
const RADIUS = 20;
const LABEL_HEIGHT = 44;
const TITLE_MAX = 24;
const LEGEND = ["done", "running", "pending", "blocked", "failed"] as const;

/** One semantic colour per state, the same tones the session dot uses. */
export const statusTone: Record<MilestoneStatus, string> = {
	done: "text-status-ready",
	running: "text-status-working",
	pending: "text-passive",
	ready: "text-passive",
	blocked: "text-status-needs-you",
	failed: "text-destructive",
	cancelled: "text-status-terminated",
};

function truncate(text: string, max: number): string {
	return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

/** The status glyph drawn inside a node, centered on (0, 0). */
export function StatusGlyph({ status }: { status: MilestoneStatus }): ReactNode {
	const icon = { x: -9, y: -9, width: 18, height: 18, strokeWidth: 2.25, "aria-hidden": true } as const;
	switch (status) {
		case "done":
			return <Check {...icon} />;
		case "running":
			return (
				<circle
					className="animate-spin"
					cx={0}
					cy={0}
					fill="none"
					r={8}
					stroke="currentColor"
					strokeDasharray="34 50"
					strokeLinecap="round"
					strokeWidth={2.25}
					style={{ transformBox: "fill-box", transformOrigin: "center" }}
				/>
			);
		case "blocked":
			return <Pause {...icon} />;
		case "failed":
			return <X {...icon} />;
		case "cancelled":
			return <Minus {...icon} />;
		default:
			return <circle cx={0} cy={0} fill="currentColor" r={4} />;
	}
}

function useNow(active: boolean): number {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (!active) return;
		const timer = window.setInterval(() => setNow(Date.now()), 15_000);
		return () => window.clearInterval(timer);
	}, [active]);
	return now;
}

export function PlanGraph({
	milestones,
	onOpen,
	onSelect,
	selectedKey,
}: {
	milestones: readonly PlanMilestone[];
	/** Opens the milestone's session (double-click or Enter). */
	onOpen: (milestone: PlanMilestone) => void;
	onSelect: (key: string) => void;
	selectedKey?: string;
}) {
	const { t } = useTranslation();
	const layout = useMemo(() => layoutPlan(milestones), [milestones]);
	const order = useMemo(() => planNodeOrder(layout), [layout]);
	const byKey = useMemo(() => new Map(milestones.map((m) => [m.key, m])), [milestones]);
	const nodeRefs = useRef(new Map<string, SVGGElement>());
	const now = useNow(milestones.some((m) => m.status === "running"));
	const statusLabel = (status: MilestoneStatus) => t(`plan.graph.status.${status}`);
	// The legend lists only the states drawn; a ready node shares the pending glyph.
	const legend = useMemo(() => {
		const shown = new Set(milestones.map((m) => (m.status === "ready" ? "pending" : m.status)));
		return LEGEND.filter((status) => shown.has(status));
	}, [milestones]);

	const position = new Map(layout.nodes.map((node) => [node.key, { x: node.x + PAD_X, y: node.y + PAD_Y }]));
	const width = Math.max(0, layout.layers - 1) * 220 + PAD_X * 2;
	const height = Math.max(0, layout.rows - 1) * 96 + PAD_Y + RADIUS + LABEL_HEIGHT;

	const secondLine = (m: PlanMilestone): string => {
		const started = m.startedAt ? Date.parse(m.startedAt) : NaN;
		if (m.status === "running") {
			const agent = m.session?.harness ? agentLabel(m.session.harness) : "";
			const elapsed = Number.isFinite(started) ? formatScriptDuration(now - started) : "";
			return [agent, elapsed].filter(Boolean).join(" · ");
		}
		if (m.status === "done") {
			const finished = m.finishedAt ? Date.parse(m.finishedAt) : NaN;
			return Number.isFinite(started) && Number.isFinite(finished) ? formatScriptDuration(finished - started) : statusLabel(m.status);
		}
		if (m.status === "pending") {
			const waiting = m.dependsOn.filter((dep) => byKey.get(dep)?.status !== "done");
			return waiting.length ? t("plan.graph.waitingFor", { keys: waiting.join(", ") }) : statusLabel(m.status);
		}
		return statusLabel(m.status);
	};

	const select = (key: string | undefined) => {
		if (!key) return;
		onSelect(key);
		nodeRefs.current.get(key)?.focus();
	};

	const onKeyDown = (event: KeyboardEvent<SVGGElement>, m: PlanMilestone) => {
		const at = order.indexOf(m.key);
		if (event.key === "ArrowRight" || event.key === "ArrowDown") {
			event.preventDefault();
			select(order[Math.min(order.length - 1, at + 1)]);
		} else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
			event.preventDefault();
			select(order[Math.max(0, at - 1)]);
		} else if (event.key === "Enter") {
			event.preventDefault();
			if (m.session) onOpen(m);
		} else if (event.key === " ") {
			event.preventDefault();
			onSelect(m.key);
		}
	};

	const focusKey = selectedKey && byKey.has(selectedKey) ? selectedKey : order[0];

	return (
		<div className="flex min-w-0 flex-col gap-4">
			<div className="overflow-x-auto">
				<svg
					aria-label={t("plan.graph.label")}
					className="block"
					data-testid="plan-graph"
					height={height}
					role="group"
					viewBox={`0 0 ${width} ${height}`}
					width={width}
				>
					{layout.edges.map((edge) => {
						const from = position.get(edge.from);
						const to = position.get(edge.to);
						if (!from || !to) return null;
						const x1 = from.x + RADIUS;
						const x2 = to.x - RADIUS;
						const bend = (x2 - x1) / 2;
						return (
							<path
								className="stroke-border-strong"
								d={`M ${x1} ${from.y} C ${x1 + bend} ${from.y}, ${x2 - bend} ${to.y}, ${x2} ${to.y}`}
								data-testid="plan-graph-edge"
								fill="none"
								key={`${edge.from}->${edge.to}`}
								strokeWidth={1.5}
							/>
						);
					})}
					{layout.nodes.map((node) => {
						const m = byKey.get(node.key);
						const at = position.get(node.key);
						if (!m || !at) return null;
						const selected = m.key === selectedKey;
						return (
							<g
								aria-label={`${m.title}: ${statusLabel(m.status)}`}
								aria-pressed={selected}
								className="cursor-pointer outline-none [&:focus-visible>circle:first-child]:stroke-ring"
								data-status={m.status}
								data-testid="plan-graph-node"
								key={m.key}
								onClick={() => onSelect(m.key)}
								onDoubleClick={() => {
									if (m.session) onOpen(m);
								}}
								onKeyDown={(event) => onKeyDown(event, m)}
								ref={(element) => {
									if (element) nodeRefs.current.set(m.key, element);
									else nodeRefs.current.delete(m.key);
								}}
								role="button"
								tabIndex={m.key === focusKey ? 0 : -1}
								transform={`translate(${at.x} ${at.y})`}
							>
								<title>{m.title}</title>
								<circle
									className={cn("fill-background", selected ? "stroke-foreground" : "stroke-border-strong")}
									r={RADIUS}
									strokeWidth={selected ? 2 : 1.5}
								/>
								<g className={statusTone[m.status]} data-testid={`plan-glyph-${m.status}`}>
									<StatusGlyph status={m.status} />
								</g>
								<text className="fill-foreground text-sm font-medium" textAnchor="middle" y={RADIUS + 18}>
									{truncate(m.title, TITLE_MAX)}
								</text>
								<text className="fill-muted-foreground text-xs" textAnchor="middle" y={RADIUS + 34}>
									{truncate(secondLine(m), 30)}
								</text>
							</g>
						);
					})}
				</svg>
			</div>
			<ul aria-label={t("plan.graph.legend")} className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
				{legend.map((status) => (
					<li className="inline-flex items-center gap-1.5" key={status}>
						<svg aria-hidden="true" className={statusTone[status]} height={18} viewBox="-9 -9 18 18" width={18}>
							<StatusGlyph status={status} />
						</svg>
						{statusLabel(status)}
					</li>
				))}
			</ul>
		</div>
	);
}
