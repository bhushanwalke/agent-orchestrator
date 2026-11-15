import { useCallback, useState, type KeyboardEventHandler, type PointerEventHandler, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import {
	DndContext,
	DragOverlay,
	KeyboardSensor,
	PointerSensor,
	pointerWithin,
	rectIntersection,
	useDraggable,
	useDroppable,
	useSensor,
	useSensors,
	type CollisionDetection,
	type DragEndEvent,
	type DragStartEvent,
	type KeyboardCoordinateGetter,
} from "@dnd-kit/core";
import {
	SessionCardView,
	getKanbanColumnView,
	type BoardLaneState,
	type BoardSessionPresentation,
	type KanbanColumn,
	type ProductUITranslator,
} from "@aoagents/product-ui";
import type { MessageKey } from "../i18n";
import {
	BOARD_MOVE_LANES,
	isBoardMoveLane,
	predictBoardMoveRefusal,
	type BoardMoveLane,
	type BoardMoveSubject,
} from "../lib/board-moves";
import { boardRefusalKey } from "../hooks/useBoardMove";
import type { Automation } from "../hooks/useAutomations";
import { AgentAvatar } from "./AgentAvatar";
import { ProductExternalLink } from "./ProductExternalLink";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSub, ContextMenuSubContent, ContextMenuSubTrigger, ContextMenuTrigger } from "./ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";

// A card moves after a deliberate 6px drag, so a click still opens it.
const POINTER_SENSOR_OPTIONS = { activationConstraint: { distance: 6 } };

// When the last drag ended. The click a browser sends after a drag released
// over the card it started on must not open that card.
let lastDragEnd = 0;
const DRAG_CLICK_WINDOW_MS = 250;

/**
 * Arrow keys carry a keyboard drag from lane to lane: Left and Right move to
 * the centre of the neighbouring lane, the only positions that mean anything
 * on this board.
 */
export const laneKeyboardCoordinates: KeyboardCoordinateGetter = (event, { context, currentCoordinates }) => {
	if (event.code !== "ArrowLeft" && event.code !== "ArrowRight") return undefined;
	const lanes = [...context.droppableRects.values()].sort((a, b) => a.left - b.left);
	if (lanes.length === 0) return undefined;
	const current = lanes.findIndex((rect) => currentCoordinates.x >= rect.left && currentCoordinates.x < rect.left + rect.width);
	const step = event.code === "ArrowRight" ? 1 : -1;
	const target = lanes[Math.min(lanes.length - 1, Math.max(0, (current < 0 ? 0 : current) + step))];
	event.preventDefault();
	return { x: target.left + target.width / 2, y: currentCoordinates.y };
};

const KEYBOARD_SENSOR_OPTIONS = {
	coordinateGetter: laneKeyboardCoordinates,
	// Space picks a card up and puts it down; Enter keeps opening it.
	keyboardCodes: { start: ["Space"], cancel: ["Escape"], end: ["Space"] },
};

// The pointer decides when it is inside a lane; a keyboard drag has none.
const laneCollision: CollisionDetection = (args) => {
	const within = pointerWithin(args);
	return within.length > 0 ? within : rectIntersection(args);
};

type BoardDragItem = { subject: BoardMoveSubject; title: string; provider: string };

/**
 * The board's drag: cards are draggable, lanes are drop targets, and a drop
 * is a move request, never a column change. While a card is dragged, the lanes
 * that would refuse it dim and say why; dropping there only shows the reason.
 */
export function BoardDndArea({
	children,
	onMove,
	onRefuse,
}: {
	children: (laneState: (column: KanbanColumn) => BoardLaneState | undefined) => ReactNode;
	onMove: (subject: BoardMoveSubject, to: BoardMoveLane) => void;
	onRefuse: (reason: NonNullable<ReturnType<typeof predictBoardMoveRefusal>>) => void;
}) {
	const sensors = useSensors(
		useSensor(PointerSensor, POINTER_SENSOR_OPTIONS),
		useSensor(KeyboardSensor, KEYBOARD_SENSOR_OPTIONS),
	);
	const [active, setActive] = useState<BoardDragItem | null>(null);
	const onDragStart = useCallback((event: DragStartEvent) => {
		setActive((event.active.data.current as BoardDragItem | undefined) ?? null);
	}, []);
	const onDragEnd = useCallback((event: DragEndEvent) => {
		lastDragEnd = Date.now();
		setActive(null);
		const item = event.active.data.current as BoardDragItem | undefined;
		const to = event.over?.id;
		if (!item || !isBoardMoveLane(to)) return;
		const refusal = predictBoardMoveRefusal(item.subject, to);
		if (refusal === "already_there" && to === currentLane(item.subject)) return;
		if (refusal) onRefuse(refusal);
		else onMove(item.subject, to);
	}, [onMove, onRefuse]);
	return (
		<DndContext collisionDetection={laneCollision} onDragCancel={() => { lastDragEnd = Date.now(); setActive(null); }} onDragEnd={onDragEnd} onDragStart={onDragStart} sensors={sensors}>
			<BoardLanes active={active}>{children}</BoardLanes>
			<DragOverlay dropAnimation={null}>
				{active ? (
					<div className="flex max-w-72 items-center gap-2.5 rounded-lg border border-border-strong bg-popover px-3 py-2 text-sm-md font-semibold text-foreground" data-testid="board-drag-preview">
						{active.provider ? <AgentAvatar provider={active.provider} /> : null}
						<span className="min-w-0 truncate">{active.title}</span>
					</div>
				) : null}
			</DragOverlay>
		</DndContext>
	);
}

function currentLane(subject: BoardMoveSubject): KanbanColumn {
	switch (subject.kind) {
		case "session":
			return subject.session.kanbanColumn ?? "building";
		case "plan":
			return subject.plan.kanbanColumn;
		case "automation":
			return "queued";
	}
}

function BoardLanes({ active, children }: { active: BoardDragItem | null; children: (laneState: (column: KanbanColumn) => BoardLaneState | undefined) => ReactNode }) {
	const { t } = useTranslation();
	// One drop target per lane; the lane set is fixed, so the hooks are too.
	const lanes = {
		queued: useDroppable({ id: "queued" }),
		building: useDroppable({ id: "building" }),
		validating: useDroppable({ id: "validating" }),
		needs_review: useDroppable({ id: "needs_review" }),
		needs_you: useDroppable({ id: "needs_you" }),
		ready: useDroppable({ id: "ready" }),
	} satisfies Record<BoardMoveLane, ReturnType<typeof useDroppable>>;
	const laneState = (column: KanbanColumn): BoardLaneState | undefined => {
		if (!isBoardMoveLane(column)) return undefined;
		const lane = lanes[column];
		const refusal = active && column !== currentLane(active.subject) ? predictBoardMoveRefusal(active.subject, column) : null;
		return {
			dropRef: lane.setNodeRef,
			refusal: refusal ? t(boardRefusalKey(refusal)) : undefined,
			isOver: lane.isOver,
		};
	};
	return <>{children(laneState)}</>;
}

/** A board card the person can drag to another lane. */
export function BoardDraggable({ children, provider, subject, title }: { children: ReactNode; provider: string; subject: BoardMoveSubject; title: string }) {
	const id = subject.kind === "session" ? subject.session.id : subject.kind === "plan" ? `plan:${subject.plan.id}` : `automation:${subject.automation.id}`;
	const { listeners, setNodeRef, isDragging } = useDraggable({ id, data: { subject, title, provider } satisfies BoardDragItem });
	return (
		<div
			className={cn("touch-none", isDragging && "opacity-50")}
			data-testid="board-draggable"
			onClickCapture={(event) => {
				if (Date.now() - lastDragEnd > DRAG_CLICK_WINDOW_MS) return;
				event.stopPropagation();
				event.preventDefault();
			}}
			onKeyDown={(event) => {
				// Only the card itself starts a keyboard drag, never a button on it.
				if ((event.target as HTMLElement).dataset.boardCardOpen === undefined) return;
				(listeners?.onKeyDown as KeyboardEventHandler<HTMLDivElement> | undefined)?.(event);
			}}
			onPointerDown={listeners?.onPointerDown as PointerEventHandler<HTMLDivElement> | undefined}
			ref={setNodeRef}
		>
			{children}
		</div>
	);
}

/**
 * "Move to" in a card's menu: the keyboard path to every move a drag makes.
 * Lanes the card cannot move to stay listed, disabled, with the reason.
 */
export function BoardMoveMenu({ onMove, subject }: { onMove: (subject: BoardMoveSubject, to: BoardMoveLane) => void; subject: BoardMoveSubject }) {
	const { t } = useTranslation();
	const translate: ProductUITranslator = (key, values) => t(key as MessageKey, values);
	const here = currentLane(subject);
	return (
		<ContextMenuSub>
			<ContextMenuSubTrigger data-testid="board-move-to">{t("board.move.moveTo")}</ContextMenuSubTrigger>
			<ContextMenuSubContent className="min-w-44">
				{BOARD_MOVE_LANES.filter((lane) => lane !== here).map((lane) => {
					const refusal = predictBoardMoveRefusal(subject, lane);
					const label = getKanbanColumnView(lane, translate).label;
					if (!refusal) {
						return <ContextMenuItem data-lane={lane} key={lane} onSelect={() => onMove(subject, lane)}>{label}</ContextMenuItem>;
					}
					return (
						<Tooltip key={lane}>
							<TooltipTrigger asChild>
								<div>
									<ContextMenuItem data-lane={lane} disabled>{label}</ContextMenuItem>
								</div>
							</TooltipTrigger>
							<TooltipContent className="max-w-64" side="right">{t(boardRefusalKey(refusal))}</TooltipContent>
						</Tooltip>
					);
				})}
			</ContextMenuSubContent>
		</ContextMenuSub>
	);
}

/** An enabled automation's next run, waiting in the queue. */
export type BoardAutomationItem = BoardSessionPresentation & { automationCard: Automation };

export function toBoardAutomationPresentation(automation: Automation, scheduledLabel: string): BoardAutomationItem {
	return {
		id: `automation:${automation.id}`,
		kanbanColumn: "queued",
		provider: automation.harness ?? "",
		status: "idle",
		displayStatus: "Scheduled",
		statusPresentation: { className: "text-muted-foreground", indicatorClassName: "bg-status-terminated", label: scheduledLabel },
		plainSurface: true,
		title: automation.displayName,
		updatedAt: automation.nextRunAt,
		automationCard: automation,
	};
}

export function isBoardAutomationItem(item: object): item is BoardAutomationItem {
	return "automationCard" in item;
}

function nextRunLabel(timestamp: string, locale?: string): string {
	const date = new Date(timestamp);
	if (Number.isNaN(date.getTime())) return "—";
	return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).format(date);
}

/** An automation's queue card: its name, "Scheduled", and the next run time. */
export function BoardAutomationCardAdapter({ item, onRunNow }: { item: BoardAutomationItem; onRunNow?: () => void }) {
	const { t, i18n } = useTranslation();
	const navigate = useNavigate();
	const translate: ProductUITranslator = (key, values) => t(key as MessageKey, values);
	const openAutomations = () => void navigate({ to: "/automations" });
	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<div data-automation-id={item.automationCard.id} data-testid="board-automation-card">
					<SessionCardView
						externalLink={ProductExternalLink}
						labels={{
							formatTime: (timestamp) => nextRunLabel(timestamp, i18n.resolvedLanguage),
							intakeIssue: (id) => t("shell.intakeIssue", { id }),
							pr: { short: "PR", states: { closed: "", draft: "", merged: "", open: "" } },
							updatedAt: (timestamp) => t("automations.nextRunAt", { time: nextRunLabel(timestamp, i18n.resolvedLanguage) }),
						}}
						onOpen={openAutomations}
						renderAvatar={(provider) => provider ? <AgentAvatar provider={provider} /> : null}
						session={item}
						translate={translate}
					/>
				</div>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-44">
				{onRunNow ? <ContextMenuItem onSelect={onRunNow}>{t("board.move.runNow")}</ContextMenuItem> : null}
				<ContextMenuItem onSelect={openAutomations}>{t("board.automation.open")}</ContextMenuItem>
			</ContextMenuContent>
		</ContextMenu>
	);
}
