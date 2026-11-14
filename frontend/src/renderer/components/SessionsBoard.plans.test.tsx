import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { toKanbanColumn } from "@aoagents/product-ui";

// Instant motion updates so height tweens do not leave tests waiting on timers.
vi.mock("motion/react", async (importOriginal) => {
	const actual = await importOriginal<typeof import("motion/react")>();
	return {
		...actual,
		AnimatePresence: ({ children }: { children: React.ReactNode }) => children,
	};
});

const {
	appMemoryMock,
	sessionMemoryMock,
	navigateMock,
	notificationShowMock,
	postMock,
	patchMock,
	workspaceQueryMock,
	cloudProjectsMock,
	spawnCloudOrchestratorMock,
	fakeRouter,
} = vi.hoisted(() => {
	// Stands in for the router's onBeforeNavigate events, which the archive host follows.
	const listeners = new Set<(event: { pathChanged: boolean }) => void>();
	return {
		routerListeners: listeners,
		fakeRouter: {
			subscribe: (_type: string, listener: (event: { pathChanged: boolean }) => void) => {
				listeners.add(listener);
				return () => {
					listeners.delete(listener);
				};
			},
		},
		appMemoryMock: vi.fn(),
		sessionMemoryMock: vi.fn(),
		navigateMock: vi.fn(),
		notificationShowMock: vi.fn(),
		postMock: vi.fn(),
		patchMock: vi.fn(),
		workspaceQueryMock: vi.fn(),
		cloudProjectsMock: vi.fn((): { id: string; config?: object }[] | undefined => undefined),
		spawnCloudOrchestratorMock: vi.fn(),
	};
});

vi.mock("../lib/cloud-orchestrator", () => ({ spawnCloudOrchestrator: spawnCloudOrchestratorMock }));

vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => ({ defaultProvider: "coder" }) }));


vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => navigateMock,
	useRouter: () => fakeRouter,
}));

vi.mock("../hooks/useWorkspaceQuery", () => ({
	workspaceQueryKey: ["workspaces"],
	remoteWorkspaceQueryKey: (hostId: string) => ["remote-workspaces", hostId],
	workspaceQueryKeyForHost: (hostId?: string) => hostId ? ["remote-workspaces", hostId] : ["workspaces"],
	cloudSessionsQueryKey: ["cloud-sessions"],
	useWorkspaceQuery: workspaceQueryMock,
	useRemoteProjectQuery: () => ({ data: undefined, isError: false, isSuccess: false }),
	useRemoteHostWorkspacesQuery: () => ({ data: undefined, isError: false, isSuccess: false }),
	useCloudProjectsQuery: () => ({ data: cloudProjectsMock() }),
	useWorkspaceScope: (projectId?: string) => {
		const query = workspaceQueryMock();
		return {
			...query,
			data: { project: query.data?.find((workspace: WorkspaceSummary) => workspace.id === projectId) },
		};
	},
}));

vi.mock("./ProjectImportOffer", () => ({ ProjectImportOffer: () => null }));

const plansMock = vi.hoisted(() => vi.fn());
vi.mock("../hooks/usePlans", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/usePlans")>()),
	usePlansQuery: () => ({ data: plansMock(), isSuccess: true }),
}));

vi.mock("./SessionTopbarActions", async (importOriginal) => ({ ...(await importOriginal<typeof import("./SessionTopbarActions")>()), SessionTopbarActions: () => null }));

vi.mock("../hooks/useSessionMemory", async (importOriginal) => {
	const { pressureState } = await import("@aoagents/product-ui");
	return {
		...(await importOriginal<typeof import("../hooks/useSessionMemory")>()),
		useAppMemory: appMemoryMock,
		useSessionMemory: sessionMemoryMock,
		// The light's own poll, fed from the same mocked reading.
		usePressureState: () => {
			const system = appMemoryMock()?.data?.system;
			return system ? pressureState(system) : undefined;
		},
	};
});

vi.mock("../lib/api-client", () => ({
	apiClient: { POST: (...args: unknown[]) => postMock(...args), PATCH: (...args: unknown[]) => patchMock(...args) },
	apiErrorCode: (error: unknown) => (error as { code?: string } | null)?.code,
	apiErrorMessage: (_error: unknown, fallback: string) => fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => undefined,
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: {
			onShortcut: () => () => {},
			onCloseShellTerminalShortcut: () => () => {},
			onPreviousTabShortcut: () => () => {},
			onNextTabShortcut: () => () => {},
			setCloseShellTerminalShortcutEnabled: () => {},
		},
		cloud: {
			getSession: vi.fn().mockResolvedValue(null),
			onSessionChanged: vi.fn(() => () => {}),
			signIn: vi.fn().mockResolvedValue(undefined),
			signOut: vi.fn().mockResolvedValue(undefined),
		},
		clipboard: {
			writeText: vi.fn(),
		},
		editorHandoff: {
			getState: vi.fn().mockResolvedValue({ targets: [], preferredEditorId: "cursor", workspaceAvailable: true }),
		},
		notifications: {
			show: (...args: unknown[]) => notificationShowMock(...args),
		},
	},
}));

vi.mock("../lib/platform", async (importOriginal) => {
	const actual = await importOriginal<typeof import("../lib/platform")>();
	return {
		...actual,
		isLinuxPlatform: () => false,
	};
});

import { useUiStore } from "../stores/ui-store";
import { SessionsBoard } from "./SessionsBoard";
import { TooltipProvider } from "./ui/tooltip";

function renderBoard(projectId?: string) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<SessionsBoard projectId={projectId} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
	return queryClient;
}

function workspaceWithSessions(sessions: WorkspaceSession[]): WorkspaceSummary {
	return {
		id: "p1",
		name: "radic",
		path: "/tmp/radic",
		kind: "single_repo",
		sessions,
	};
}

function boardSession(
	overrides: Pick<WorkspaceSession, "id" | "title" | "status"> & Partial<WorkspaceSession>,
): WorkspaceSession {
	return {
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		branch: `ao/${overrides.id}`,
		kanbanColumn: toKanbanColumn(undefined, overrides.status),
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

function plan(overrides: Record<string, unknown> = {}) {
	return {
		id: "plan_1",
		projectId: "p1",
		title: "TODO app MVP",
		ticketRef: "",
		state: "active",
		phase: "implementing",
		kanbanColumn: "building",
		displayStatus: "Implementing 1/3",
		milestoneCounts: { total: 4, done: 1, running: 2, failed: 0, blocked: 0 },
		liveSessions: [
			{ id: "w-1", harness: "codex", status: "working", kanbanColumn: "building", displayStatus: "Working", isTerminated: false },
			{ id: "w-2", harness: "claude-code", status: "working", kanbanColumn: "building", displayStatus: "Working", isTerminated: false },
		],
		prs: [{ url: "https://github.com/o/r/pull/7", number: 7, state: "open" }],
		orchestratorSessionId: "orch-1",
		maxParallel: 3,
		createdAt: "2026-01-01T00:00:00Z",
		updatedAt: "2026-01-02T00:00:00Z",
		...overrides,
	};
}

function columnOf(name: string) {
	return screen.getAllByTestId("board-column").find((column) => column.getAttribute("data-column") === name)!;
}

beforeEach(() => {
	useUiStore.setState({ developerMode: false, diagnostics: false, archiveModal: null, showPlanSessions: false });
	navigateMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: {} });
	workspaceQueryMock.mockReset();
	appMemoryMock.mockReset().mockReturnValue({ data: undefined, isError: false });
	sessionMemoryMock.mockReset().mockReturnValue({ data: undefined, isError: false });
	plansMock.mockReset().mockReturnValue([plan()]);
	workspaceQueryMock.mockReturnValue({
		data: [workspaceWithSessions([
			boardSession({ id: "solo", title: "Solo worker", status: "working" }),
			boardSession({ id: "w-1", title: "TODO app MVP · m1: API", status: "working", planId: "plan_1", planMilestoneId: "ms_1" }),
		])],
		isSuccess: true,
	});
});

describe("SessionsBoard plans", () => {
	it("shows a Task card in its lane with milestones, agents, and PRs", () => {
		renderBoard("p1");
		const working = columnOf("building");
		const card = within(working).getByTestId("board-plan-card");
		expect(within(card).getByText("TODO app MVP")).toBeInTheDocument();
		expect(within(card).getByText("Implementing 1/3")).toBeInTheDocument();
		// The count is a compact metric so the status phrase keeps its width.
		const metric = within(card).getByTestId("plan-milestone-metric");
		expect(metric).toHaveTextContent("1/4");
		expect(metric).toHaveAttribute("title", "1 of 4 milestones done");
		expect(within(metric).getByText("1 of 4 milestones done")).toHaveClass("sr-only");
		expect(within(card).getAllByTestId("plan-agent-avatar")).toHaveLength(2);
		expect(within(card).getByText("#7")).toBeInTheDocument();
		expect(within(card).queryByText(/ao\//)).toBeNull();
		// The lane counts the Task and the visible session, not the folded one.
		expect(within(working).getByText("2")).toBeInTheDocument();
	});

	it("folds a Task's agent sessions until they are asked for", async () => {
		renderBoard("p1");
		expect(screen.queryByText("TODO app MVP · m1: API")).toBeNull();
		expect(screen.getByText("Solo worker")).toBeInTheDocument();
		const toggle = screen.getByRole("button", { name: "Show agent sessions" });
		expect(toggle).toHaveAttribute("aria-pressed", "false");
		await userEvent.click(toggle);
		expect(screen.getByText("TODO app MVP · m1: API")).toBeInTheDocument();
		expect(useUiStore.getState().showPlanSessions).toBe(true);
		expect(screen.getByRole("button", { name: "Show agent sessions" })).toHaveAttribute("aria-pressed", "true");
	});

	it("folds a research worker attached to the Task without a milestone", () => {
		workspaceQueryMock.mockReturnValue({
			data: [workspaceWithSessions([
				boardSession({ id: "solo", title: "Solo worker", status: "working" }),
				boardSession({ id: "r-1", title: "Research: storage", status: "needs_input", planId: "plan_1" }),
			])],
			isSuccess: true,
		});
		renderBoard("p1");
		expect(screen.getByTestId("board-plan-card")).toBeInTheDocument();
		expect(screen.queryByText("Research: storage")).toBeNull();
		expect(screen.getByText("Solo worker")).toBeInTheDocument();
	});

	it("places Tasks by their lane and leaves cancelled ones off the board", () => {
		plansMock.mockReturnValue([
			plan({ id: "plan_done", title: "Shipped task", phase: "done", kanbanColumn: "ready", displayStatus: "QA passed" }),
			plan({ id: "plan_x", title: "Dropped task", phase: "cancelled", state: "cancelled", kanbanColumn: "archive", displayStatus: "Cancelled" }),
		]);
		renderBoard("p1");
		expect(within(columnOf("ready")).getByText("Shipped task")).toBeInTheDocument();
		expect(screen.queryByText("Dropped task")).toBeNull();
	});

	it("opens the run view, and offers orchestrator, cancel, and copy actions", async () => {
		renderBoard("p1");
		const card = screen.getByTestId("board-plan-card");
		await userEvent.click(within(card).getByRole("button", { name: "TODO app MVP" }));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/plans/$planId", params: { projectId: "p1", planId: "plan_1" } });

		fireEvent.contextMenu(card);
		expect(await screen.findByRole("menuitem", { name: "Open orchestrator" })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: "Copy plan id" })).toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Cancel plan" }));
		const dialog = await screen.findByRole("dialog");
		await userEvent.click(within(dialog).getByRole("button", { name: "Cancel plan" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/plans/{planId}/cancel", { params: { path: { planId: "plan_1" } } }));
	});
});
