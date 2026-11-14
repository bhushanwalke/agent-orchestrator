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
	useWorkspaceQuery: () => ({ data: [{ id: "p1", name: "radic", path: "/tmp/radic", kind: "single_repo", sessions }], isSuccess: true }),
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

const sessions: WorkspaceSession[] = [session("orch-1", "radic orchestrator"), session("w-1", "TODO app · m1: API")];

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
			kanbanColumn: "building",
			displayStatus: "Implementing 0/2",
			milestoneCounts: { total: 3, done: 0, running: 1, failed: 0, blocked: 0 },
			liveSessions: [{ id: "w-1", harness: "codex", status: "working", kanbanColumn: "building", displayStatus: "Working", isTerminated: false }],
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
	navigateMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: {} });
	planMock.mockReset().mockReturnValue(detail());
});

describe("PlanRunView", () => {
	it("heads the page with the title, phase, start line, and ticket", () => {
		renderView();
		expect(screen.getByRole("heading", { name: "TODO app" })).toBeInTheDocument();
		expect(screen.getByTestId("plan-phase")).toHaveTextContent("Implementing");
		expect(screen.getByText(/· 1 agents active$/)).toBeInTheDocument();
		expect(screen.getByRole("link", { name: /Ticket/ })).toHaveAttribute("href", "https://example.com/T-1");
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

	it("lists events newest first", () => {
		renderView();
		expect(screen.getAllByTestId("plan-event").map((row) => row.textContent)).toEqual([
			expect.stringMatching(/^API started/),
			expect.stringMatching(/^Plan started/),
			expect.stringMatching(/^Task created/),
		]);
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
