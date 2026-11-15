import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toKanbanColumn } from "@aoagents/product-ui";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { PlanDetail, PlanMilestone } from "../../types/plan";
import type { WorkspaceSession } from "../../types/workspace";

const { navigateMock, postMock, planMock } = vi.hoisted(() => ({
	navigateMock: vi.fn(),
	postMock: vi.fn(),
	planMock: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({ useNavigate: () => navigateMock }));

vi.mock("../../hooks/usePlans", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/usePlans")>()),
	usePlanQuery: () => ({ data: planMock(), isError: false }),
}));

vi.mock("../../hooks/useWorkspaceQuery", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../hooks/useWorkspaceQuery")>()),
	useWorkspaceQuery: () => ({ data: [{ id: "p1", name: "radic", path: "/tmp/radic", kind: "single_repo", sessions: sessionsOverride ?? sessions }], isSuccess: true }),
}));

vi.mock("../../lib/api-client", async (importOriginal) => ({
	...(await importOriginal<typeof import("../../lib/api-client")>()),
	apiClient: {
		GET: vi.fn().mockResolvedValue({ data: undefined }),
		POST: (...args: unknown[]) => postMock(...args),
	},
}));

import { TooltipProvider } from "../ui/tooltip";
import { PlanRunView } from "./PlanRunView";

function session(id: string, title: string): WorkspaceSession {
	return {
		id,
		title,
		status: "working",
		workspaceId: "p1",
		workspaceName: "radic",
		provider: "claude-code",
		branch: `ao/${id}`,
		kanbanColumn: toKanbanColumn(undefined, "working"),
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
	};
}

let sessionsOverride: WorkspaceSession[] | undefined;

const sessions: WorkspaceSession[] = [
	session("orch-1", "radic orchestrator"),
	{ ...session("w-1", "TODO app · m1: API"), activity: { state: "active" } as WorkspaceSession["activity"] },
	{ ...session("w-2", "TODO app · m2: UI"), activity: { state: "idle" } as WorkspaceSession["activity"] },
	{ ...session("r-1", "Research: storage"), activity: { state: "idle" } as WorkspaceSession["activity"] },
];

function milestone(key: string, status: PlanMilestone["status"], overrides: Partial<PlanMilestone> = {}): PlanMilestone {
	return {
		id: `ms_${key}`,
		key,
		kind: key === "qa" ? "qa" : "implement",
		ordinal: Number(key.replace(/\D/g, "")) || 9,
		title: `Milestone ${key}`,
		description: "",
		status,
		dependsOn: [],
		attempt: 1,
		branch: "",
		prs: [],
		qaNote: "",
		qaVerdict: "",
		sparsePaths: [],
		createdAt: "2026-01-01T00:00:00Z",
		updatedAt: "2026-01-01T00:00:00Z",
		...overrides,
	};
}

function detail(overrides: Partial<PlanDetail["plan"]> = {}): PlanDetail {
	return {
		plan: {
			id: "plan_1",
			projectId: "p1",
			title: "TODO app",
			brief: "Build it",
			ticketRef: "https://example.com/T-1",
			specMarkdown: "# Spec\n\nThe plan.",
			state: "active",
			phase: "implementing",
			intakeMode: "plan",
			kanbanColumn: "building",
			displayStatus: "Implementing 0/2",
			milestoneCounts: { total: 3, done: 0, running: 1, failed: 0, blocked: 0 },
			liveSessions: [
				{ id: "w-1", harness: "codex", status: "working", kanbanColumn: "building", displayStatus: "Working", isTerminated: false },
				{ id: "w-2", harness: "codex", status: "idle", kanbanColumn: "building", displayStatus: "Idle", isTerminated: false },
			],
			prs: [],
			orchestratorSessionId: "orch-1",
			maxParallel: 3,
			createdAt: "2026-01-01T00:00:00Z",
			updatedAt: "2026-01-01T00:00:00Z",
			...overrides,
		},
		milestones: [
			milestone("m1", "running", { title: "API", session: { id: "w-1", harness: "codex", status: "working", displayStatus: "Working", kanbanColumn: "building", isTerminated: false } }),
			milestone("m2", "pending", { title: "UI", dependsOn: ["m1"] }),
			milestone("qa", "pending", { title: "QA", dependsOn: ["m2"] }),
		],
		researchSessions: [],
		events: [
			{ id: 3, kind: "milestone_started", milestoneKey: "m1", sessionId: "w-1", note: "", createdAt: "2026-01-01T00:02:00Z" },
			{ id: 2, kind: "plan_activated", milestoneKey: "", sessionId: "", note: "", createdAt: "2026-01-01T00:01:00Z" },
			{ id: 1, kind: "plan_created", milestoneKey: "", sessionId: "", note: "", createdAt: "2026-01-01T00:00:00Z" },
		],
	};
}

function renderView() {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<PlanRunView planId="plan_1" projectId="p1" />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	sessionsOverride = undefined;
	navigateMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: {} });
	planMock.mockReset().mockReturnValue(detail());
});

describe("PlanRunView", () => {
	it("heads the page with the title, phase, start line, and ticket", () => {
		renderView();
		expect(screen.getByRole("heading", { name: "TODO app" })).toBeInTheDocument();
		expect(screen.getByTestId("plan-phase")).toHaveTextContent("Implementing");
		expect(screen.getByText(/· 1 agent active$/)).toBeInTheDocument();
		expect(screen.getByRole("link", { name: /Ticket/ })).toHaveAttribute("href", "https://example.com/T-1");
	});

	it("counts only agents that are working, so a finished Task reads 0 agents active", () => {
		planMock.mockReturnValue(
			detail({ phase: "done", liveSessions: [{ id: "w-2", harness: "codex", status: "idle", kanbanColumn: "ready", displayStatus: "Idle", isTerminated: false }] }),
		);
		renderView();
		expect(screen.getByText(/· 0 agents active$/)).toBeInTheDocument();
	});

	it("pluralises the active agent count", () => {
		const live = (id: string) => ({ id, harness: "codex", status: "working", kanbanColumn: "building", displayStatus: "Working", isTerminated: false }) as const;
		const busy = sessions.map((s) => (s.id === "w-2" ? { ...s, activity: { state: "active" } as WorkspaceSession["activity"] } : s));
		sessionsOverride = busy;
		planMock.mockReturnValue(detail({ liveSessions: [live("w-1"), live("w-2")] }));
		renderView();
		expect(screen.getByText(/· 2 agents active$/)).toBeInTheDocument();
	});

	it("lists the orchestrator first, then each milestone's agent, and opens a session from its card", async () => {
		renderView();
		const cards = screen.getAllByTestId("plan-agent-card");
		expect(cards).toHaveLength(2);
		expect(within(cards[0]).getByText("Orchestrator")).toBeInTheDocument();
		expect(within(cards[1]).getByText("m1 · API")).toBeInTheDocument();
		await userEvent.click(within(cards[1]).getByText("TODO app · m1: API"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "p1", sessionId: "w-1" } });
	});

	it("lists research workers under the milestone agents in their own Research section", async () => {
		const research = { id: "r-1", harness: "codex", status: "idle", kanbanColumn: "validating", displayStatus: "Idle", isTerminated: false, branch: "ao/r-1" } as const;
		const killed = { ...research, id: "r-killed", status: "terminated", isTerminated: true } as const;
		// The workspace list keeps a killed session; the board drops it as archived.
		sessionsOverride = [...sessions, { ...session("r-killed", "Research: old question"), status: "terminated", isTerminated: true }];
		planMock.mockReturnValue({ ...detail(), researchSessions: [research, killed] });
		renderView();
		expect(screen.getByRole("button", { name: "Research", expanded: true })).toBeInTheDocument();
		// A killed research worker has left the board, so it leaves the rail too.
		expect(screen.queryByText("Research: old question")).toBeNull();
		const cards = screen.getAllByTestId("plan-research-card");
		expect(cards).toHaveLength(1);
		expect(screen.getAllByTestId("plan-agent-card")).toHaveLength(2);
		await userEvent.click(within(cards[0]).getByText("Research: storage"));
		expect(navigateMock).toHaveBeenCalledWith({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: "p1", sessionId: "r-1" } });
	});

	it("omits the Research section when the Task has no research workers", () => {
		renderView();
		expect(screen.queryByRole("button", { name: "Research" })).toBeNull();
	});

	it("titles the rail sections in sentence case, as the inspector does", () => {
		renderView();
		expect(screen.getByRole("button", { name: "Agents", expanded: true })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Recent events", expanded: true })).toBeInTheDocument();
		expect(document.querySelector(".eyebrow-label")).toBeNull();
	});

	it("shows a milestone agent's recorded work branch rather than its session branch", () => {
		const d = detail();
		planMock.mockReturnValue({ ...d, milestones: d.milestones.map((m) => (m.key === "m1" ? { ...m, branch: "topic/api-work" } : m)) });
		renderView();
		const card = screen.getAllByTestId("plan-agent-card")[1];
		expect(within(card).getByText("topic/api-work")).toBeInTheDocument();
		expect(within(card).queryByText("w-1")).toBeNull();
	});

	it("lists events newest first", () => {
		renderView();
		expect(screen.getAllByTestId("plan-event").map((row) => row.textContent)).toEqual([
			expect.stringMatching(/^API started/),
			expect.stringMatching(/^Plan started/),
			expect.stringMatching(/^Task created/),
		]);
	});

	it("keeps an event's verb visible and truncates only the milestone title", () => {
		renderView();
		const row = screen.getAllByTestId("plan-event")[0];
		const title = within(row).getByText("API");
		expect(title).toHaveClass("truncate");
		expect(title.parentElement).toHaveTextContent(/^API started$/);
		expect(within(row).getByText("started")).not.toHaveClass("truncate");
	});

	it("draws a historical start with the neutral dot, not the running arc", () => {
		renderView();
		const icon = screen.getAllByTestId("plan-event")[0].querySelector("svg");
		expect(icon).toHaveClass("text-passive");
		expect(icon?.querySelector(".animate-spin")).toBeNull();
	});

	it("labels a kickoff held for a starting orchestrator as neutral waiting", () => {
		const d = detail();
		planMock.mockReturnValue({ ...d, events: [{ id: 9, kind: "kickoff_waiting", milestoneKey: "", sessionId: "orch-1", note: "", createdAt: "2026-01-01T00:00:01Z" }, ...d.events] });
		renderView();
		const row = screen.getAllByTestId("plan-event")[0];
		expect(row).toHaveTextContent(/^Starting the orchestrator/);
		expect(row.querySelector("svg")).toHaveClass("text-passive");
	});

	it("cancels the plan only after confirming", async () => {
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		expect(screen.getByRole("menuitem", { name: "Re-run QA" })).toHaveAttribute("aria-disabled", "true");
		await userEvent.click(screen.getByRole("menuitem", { name: "Cancel plan" }));
		const dialog = await screen.findByRole("dialog");
		expect(postMock).not.toHaveBeenCalled();
		await userEvent.click(within(dialog).getByRole("button", { name: "Cancel plan" }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/plans/{planId}/cancel", { params: { path: { planId: "plan_1" } } }));
	});

	it("re-runs QA once the Task is done", async () => {
		planMock.mockReturnValue(detail({ phase: "done" }));
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Re-run QA" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/plans/{planId}/milestones/{key}/retry", { params: { path: { planId: "plan_1", key: "qa" } } }),
		);
	});
});

function triageDetail(overrides: Partial<PlanDetail["plan"]> = {}, events: PlanDetail["events"] = []): PlanDetail {
	return {
		...detail({ state: "triage", phase: "triage", intakeMode: "auto", displayStatus: "Deciding", kanbanColumn: "building", specMarkdown: "", milestoneCounts: { total: 0, done: 0, running: 0, failed: 0, blocked: 0 }, liveSessions: [], ...overrides }),
		milestones: [],
		events,
	};
}

describe("PlanRunView triage", () => {
	it("waits for the orchestrator's decision with no graph or legend", () => {
		planMock.mockReturnValue(triageDetail());
		renderView();
		expect(screen.getByTestId("plan-phase")).toHaveTextContent("Deciding");
		expect(screen.getByText("Waiting for the orchestrator to decide how to run this Task")).toBeInTheDocument();
		expect(screen.queryByRole("list", { name: "Milestone states" })).toBeNull();
		expect(screen.queryByText("The orchestrator hasn't added milestones yet.")).toBeNull();
	});

	it("plans the Task from Actions without a confirmation", async () => {
		planMock.mockReturnValue(triageDetail());
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Plan it" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/plans/{planId}/triage", { params: { path: { planId: "plan_1" } }, body: { decision: "plan" } }),
		);
	});

	it("sends the Task to one worker only after confirming", async () => {
		planMock.mockReturnValue(triageDetail());
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		await userEvent.click(screen.getByRole("menuitem", { name: "Send to one worker" }));
		const dialog = await screen.findByRole("dialog");
		expect(postMock).not.toHaveBeenCalled();
		await userEvent.click(within(dialog).getByRole("button", { name: "Send to one worker" }));
		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith("/api/v1/plans/{planId}/triage", { params: { path: { planId: "plan_1" } }, body: { decision: "direct" } }),
		);
	});

	it("offers no triage actions once a Task is planning", async () => {
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		expect(screen.queryByRole("menuitem", { name: "Plan it" })).toBeNull();
		expect(screen.queryByRole("menuitem", { name: "Send to one worker" })).toBeNull();
	});

	it("names the triage events in sentence case", () => {
		planMock.mockReturnValue(triageDetail({ triage: { decision: "plan", reason: "API and UI both change", decidedBy: "orchestrator", decidedAt: "2026-01-01T00:02:00Z" } }, [
			{ id: 6, kind: "direct_spawn_failed", milestoneKey: "", sessionId: "", note: "worktree busy", createdAt: "2026-01-01T00:05:00Z" },
			{ id: 5, kind: "triage_reminder", milestoneKey: "", sessionId: "orch-1", note: "", createdAt: "2026-01-01T00:04:00Z" },
			{ id: 4, kind: "triage_decided", milestoneKey: "", sessionId: "", note: "plan, decided by orchestrator: API and UI both change", createdAt: "2026-01-01T00:02:00Z" },
			{ id: 3, kind: "triage_requested", milestoneKey: "", sessionId: "", note: "", createdAt: "2026-01-01T00:01:00Z" },
		]));
		renderView();
		const rows = screen.getAllByTestId("plan-event");
		expect(rows.map((row) => row.textContent)).toEqual([
			expect.stringMatching(/^Worker could not start: worktree busy/),
			expect.stringMatching(/^Reminder sent to the orchestrator/),
			expect.stringMatching(/^Decided: plan — API and UI both change/),
			expect.stringMatching(/^Triage requested/),
		]);
		expect(rows[0].querySelector("svg")).toHaveClass("text-destructive");
	});
});

describe("PlanRunView direct", () => {
	const direct = () => triageDetail({
		state: "direct", phase: "direct", displayStatus: "One worker", kanbanColumn: "archive", directSessionId: "w-9",
		triage: { decision: "direct", reason: "One small rename with its test", decidedBy: "orchestrator", decidedAt: "2026-01-01T00:02:00Z" },
	}, [
		{ id: 5, kind: "direct_spawned", milestoneKey: "", sessionId: "w-9", note: "", createdAt: "2026-01-01T00:03:00Z" },
		{ id: 4, kind: "triage_decided", milestoneKey: "", sessionId: "", note: "direct, decided by orchestrator: One small rename with its test", createdAt: "2026-01-01T00:02:00Z" },
	]);

	it("says one worker handles it, with the reason and a way to its session", async () => {
		planMock.mockReturnValue(direct());
		renderView();
		expect(screen.getByTestId("plan-phase")).toHaveTextContent("One worker");
		expect(screen.getByText("Handled by a single worker")).toBeInTheDocument();
		expect(screen.getByText("One small rename with its test", { selector: "p" })).toBeInTheDocument();
		expect(screen.queryByRole("list", { name: "Milestone states" })).toBeNull();
		await userEvent.click(screen.getByRole("button", { name: "Open worker session" }));
		expect(navigateMock).toHaveBeenCalledTimes(1);
		expect(JSON.stringify(navigateMock.mock.calls[0][0])).toContain("w-9");
	});

	it("keeps only the orchestrator chat in Actions while the spec is empty", async () => {
		planMock.mockReturnValue(direct());
		renderView();
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["Open orchestrator chat"]);
	});

	it("names the decision and the worker start", () => {
		planMock.mockReturnValue(direct());
		renderView();
		expect(screen.getAllByTestId("plan-event").map((row) => row.textContent)).toEqual([
			expect.stringMatching(/^Worker started/),
			expect.stringMatching(/^Decided: one worker — One small rename with its test/),
		]);
	});
});
