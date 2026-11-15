import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import type { PlanSummary } from "../types/plan";
import type { Automation } from "../hooks/useAutomations";

const { navigateMock, postMock, workspaceQueryMock, plansMock, automationsMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	postMock: vi.fn(),
	workspaceQueryMock: vi.fn(),
	plansMock: vi.fn((): PlanSummary[] | undefined => undefined),
	automationsMock: vi.fn((): Automation[] | undefined => undefined),
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useRouter: () => ({ subscribe: () => () => {} }),
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	useWorkspaceQuery: workspaceQueryMock,
	useRemoteProjectQuery: () => ({ data: undefined, isError: false, isSuccess: false }),
	useRemoteHostWorkspacesQuery: () => ({ data: undefined, isError: false, isSuccess: false }),
	useCloudProjectsQuery: () => ({ data: undefined }),
	useWorkspaceScope: () => ({ data: { project: undefined } }),
}));

vi.mock("../hooks/usePlans", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/usePlans")>()),
	usePlansQuery: () => ({ data: plansMock() }),
}));

vi.mock("../hooks/useAutomations", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useAutomations")>()),
	useAutomations: () => ({ data: automationsMock() }),
}));

vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ defaultProvider: "coder" }) }));
vi.mock("./ProjectImportOffer", () => ({ ProjectImportOffer: () => null }));
vi.mock("../hooks/useSessionMemory", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/useSessionMemory")>()),
	useAppMemory: () => ({ data: undefined, isError: false }),
	useSessionMemory: () => ({ data: undefined, isError: false }),
	usePressureState: () => undefined,
}));

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: (...args: unknown[]) => postMock(...args), PATCH: vi.fn(), GET: vi.fn().mockResolvedValue({ data: undefined }) },
	apiErrorCode: (error: unknown) => (error as { code?: string } | null)?.code,
	apiErrorMessage: (_error: unknown, fallback = "Request failed") => fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => undefined,
}));

import { useUiStore } from "../stores/ui-store";
import { SessionsBoard } from "./SessionsBoard";
import { TooltipProvider } from "./ui/tooltip";
import { appI18n } from "../i18n";

function renderBoard() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<SessionsBoard projectId="p1" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

function workspace(sessions: WorkspaceSession[]): WorkspaceSummary {
	return { id: "p1", name: "radic", path: "/tmp/radic", kind: "single_repo", sessions };
}

function worker(overrides: Partial<WorkspaceSession> & Pick<WorkspaceSession, "id" | "title">): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		kind: "worker",
		status: "idle",
		kanbanColumn: "building",
		kanbanStage: "building",
		displayStatus: "Idle",
		updatedAt: "2026-11-15T10:00:00Z",
		prs: [],
		...overrides,
	};
}

function automation(overrides: Partial<Automation> = {}): Automation {
	return {
		id: "auto-1",
		projectId: "p1",
		displayName: "Nightly triage",
		enabled: true,
		nextRunAt: "2026-11-16T09:00:00Z",
		harness: "claude-code",
		taskMode: "direct",
		...overrides,
	} as Automation;
}

function lane(name: string) {
	return screen.getByRole("region", { name: `${name} sessions` });
}

beforeEach(() => {
	useUiStore.setState({ globalToasts: [] });
	navigateMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: {} });
	plansMock.mockReset().mockReturnValue([]);
	automationsMock.mockReset().mockReturnValue([]);
	workspaceQueryMock.mockReset().mockReturnValue({ data: [], isError: false, isSuccess: true });
});

describe("SessionsBoard moves", () => {
	it("queues an enabled automation of this project as a Scheduled card, and no disabled or foreign one", () => {
		workspaceQueryMock.mockReturnValue({ data: [workspace([worker({ id: "s1", title: "worker one" })])], isError: false, isSuccess: true });
		automationsMock.mockReturnValue([
			automation(),
			automation({ id: "auto-off", displayName: "Paused job", enabled: false }),
			automation({ id: "auto-other", displayName: "Other project job", projectId: "p2" }),
		]);
		renderBoard();
		const queue = lane("In queue");
		const card = within(queue).getByTestId("board-automation-card");
		expect(card).toHaveTextContent("Nightly triage");
		expect(card).toHaveTextContent("Scheduled");
		expect(screen.queryByText("Paused job")).toBeNull();
		expect(screen.queryByText("Other project job")).toBeNull();
	});

	it("prints the stage before the reason on a Needs you card", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspace([worker({ id: "s1", title: "review me", kanbanColumn: "needs_you", kanbanStage: "needs_review", displayStatus: "Changes requested" })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard();
		expect(within(lane("Needs you")).getByTestId("session-status")).toHaveTextContent("In review · Changes requested");
	});

	it("shows the empty-lane hints", () => {
		workspaceQueryMock.mockReturnValue({ data: [workspace([worker({ id: "s1", title: "worker one" })])], isError: false, isSuccess: true });
		renderBoard();
		expect(within(lane("In queue")).getByTestId("board-column-hint")).toHaveTextContent("Nothing waiting to start.");
		expect(within(lane("Needs you")).getByTestId("board-column-hint")).toHaveTextContent("Nothing needs you.");
	});

	it("moves a card from its menu, disabling refused lanes, and toasts the daemon's refusal", async () => {
		workspaceQueryMock.mockReturnValue({ data: [workspace([worker({ id: "s1", title: "finished work", kanbanColumn: "needs_you", kanbanStage: "building", displayStatus: "Awaiting review" })])], isError: false, isSuccess: true });
		postMock.mockResolvedValue({ error: { code: "BOARD_MOVE_UNSUPPORTED", message: "no provider", details: { reason: "no_pr_provider" } } });
		renderBoard();
		const card = within(lane("Needs you")).getByTestId("board-session-card");
		fireEvent.contextMenu(card);
		fireEvent.click(await screen.findByTestId("board-move-to"));
		fireEvent.keyDown(screen.getByTestId("board-move-to"), { key: "ArrowRight" });
		const queued = await screen.findByRole("menuitem", { name: /^In queue/ });
		expect(queued).toHaveAttribute("data-disabled");
		expect(screen.getByRole("menuitem", { name: /^In review/ })).toHaveAttribute("data-disabled");
		fireEvent.click(screen.getByRole("menuitem", { name: "Validating" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/board-move", { params: { path: { sessionId: "s1" } }, body: { to: "validating" } }));
		await waitFor(() => expect(useUiStore.getState().globalToasts.map((toast) => toast.title)).toContain(appI18n.t("board.move.refused.no_pr_provider")));
	});

	it("translates a Task card's status, counts included", async () => {
		workspaceQueryMock.mockReturnValue({ data: [workspace([])], isError: false, isSuccess: true });
		plansMock.mockReturnValue([
			{ id: "plan_1", projectId: "p1", title: "Held task", phase: "planning", state: "planning", kanbanColumn: "queued", kanbanStage: "building", displayStatus: "On hold", milestoneCounts: { total: 0, done: 0, running: 0, failed: 0, blocked: 0 }, liveSessions: [], prs: [], updatedAt: "2026-11-15T10:00:00Z", createdAt: "2026-11-15T10:00:00Z" },
			{ id: "plan_2", projectId: "p1", title: "Busy task", phase: "implementing", state: "active", kanbanColumn: "building", kanbanStage: "building", displayStatus: "Implementing 1/3", milestoneCounts: { total: 3, done: 1, running: 1, failed: 0, blocked: 0 }, liveSessions: [], prs: [], updatedAt: "2026-11-15T10:00:00Z", createdAt: "2026-11-15T10:00:00Z" },
		] as unknown as PlanSummary[]);
		await appI18n.changeLanguage("de");
		try {
			renderBoard();
			const held = screen.getByText("Held task").closest("[data-testid='board-plan-card']") as HTMLElement;
			expect(within(held).getByTestId("session-status")).toHaveTextContent(appI18n.t("displayStatus.onHold"));
			expect(within(held).getByTestId("session-status")).not.toHaveTextContent("On hold");
			const busy = screen.getByText("Busy task").closest("[data-testid='board-plan-card']") as HTMLElement;
			expect(within(busy).getByTestId("session-status")).toHaveTextContent("Umsetzung 1/3");
		} finally {
			await appI18n.changeLanguage("en");
		}
	});

	it("opens the menu from Shift+F10 and the menu key, gives refused lanes their reason inline, and returns focus to the card", async () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspace([worker({ id: "s1", title: "finished work", kanbanColumn: "needs_you", kanbanStage: "building", displayStatus: "Awaiting review", hasPrProvider: false })])],
			isError: false,
			isSuccess: true,
		});
		renderBoard();
		const open = within(lane("Needs you")).getByRole("button", { name: "finished work" });
		open.focus();
		fireEvent.keyDown(open, { key: "F10", shiftKey: true });
		fireEvent.click(await screen.findByTestId("board-move-to"));
		fireEvent.keyDown(screen.getByTestId("board-move-to"), { key: "ArrowRight" });
		const validating = await screen.findByRole("menuitem", { name: /^Validating/ });
		expect(validating).toHaveAttribute("data-disabled");
		expect(within(validating).getByTestId("board-move-refusal")).toHaveTextContent(appI18n.t("board.move.refused.no_pr_provider"));
		fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
		fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
		await waitFor(() => expect(screen.queryByTestId("board-move-to")).toBeNull());
		await waitFor(() => expect(document.activeElement).toBe(within(lane("Needs you")).getByRole("button", { name: "finished work" })));

		fireEvent.keyDown(open, { key: "ContextMenu" });
		expect(await screen.findByTestId("board-move-to")).toBeInTheDocument();
	});

	it("runs an automation now from its card's menu", async () => {
		workspaceQueryMock.mockReturnValue({ data: [workspace([])], isError: false, isSuccess: true });
		automationsMock.mockReturnValue([automation()]);
		renderBoard();
		fireEvent.contextMenu(within(lane("In queue")).getByTestId("board-automation-card"));
		fireEvent.click(await screen.findByRole("menuitem", { name: "Run now" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/automations/{automationId}/run-now", { params: { path: { automationId: "auto-1" } } }));
	});
});
