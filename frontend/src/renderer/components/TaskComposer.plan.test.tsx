import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChatWorkspaceProps } from "./chat/ChatWorkspace";
import type { ApprovalMode } from "../types/conversation";
import { NewChatPage } from "./NewChatPage";
import { TooltipProvider } from "./ui/tooltip";

const h = vi.hoisted(() => ({
	navigate: vi.fn(),
	launch: vi.fn(),
	launchFallback: vi.fn(),
	fallbackAction: undefined as string | undefined,
	isCloudProject: false,
	error: undefined as string | undefined,
	status: undefined as string | undefined,
	errorAction: undefined as { label: string; onClick: () => void; input?: { label: string; value: string; onChange: (value: string) => void } } | undefined,
	replay: { agent: "codex", model: "gpt-6", effort: "high", approvalMode: undefined as string | undefined, mode: undefined as string | undefined },
	workspace: undefined as ChatWorkspaceProps | undefined,
	harness: "codex",
	approvalMode: "auto" as ApprovalMode,
	approvalModes: undefined as ApprovalMode[] | undefined,
	modelWarning: undefined as string | undefined,
	modelWarningRetries: true,
	offering: { ready: false, loaded: false, org: undefined as { id: string } | undefined, features: [] as string[], maxAttachmentBytes: undefined as number | undefined },
	workspaces: [] as Array<Record<string, unknown>>,
	createPlan: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
	useNavigate: () => h.navigate,
	useRouter: () => ({ preloadRoute: async () => undefined, history: { canGoBack: () => false, back: vi.fn() } }),
}));
vi.mock("./settings/MobileDevicesSection", () => ({ mobileDevicesQueryKey: ["mobile-devices"], fetchDevices: async () => [] }));
vi.mock("./chat/ChatWorkspace", () => ({
	ChatWorkspace: (props: ChatWorkspaceProps) => {
		h.workspace = props;
		return <>{props.draftNotice}</>;
	},
}));
vi.mock("./TaskComposer", () => ({
	useTaskLaunch: () => ({
		launch: h.launch,
		launchFallback: h.launchFallback,
		fallbackAction: h.fallbackAction,
		fallbackLabel: "Create as Terminal UI",
		error: h.error,
		status: h.status,
		errorAction: h.errorAction,
		submitMode: undefined,
		isCloudProject: h.isCloudProject,
		canSubmit: true,
		catalogProjectId: "proj-1",
		launchSnapshot: () => ({ ...h.replay }),
		effectiveInterface: "chat",
		refreshModels: async () => undefined,
		modelWarning: h.modelWarning,
		modelWarningRetries: h.modelWarningRetries,
		chatModels: [{ id: "gpt-6", displayName: "GPT-6", default: true }],
		turnSettings: { model: "gpt-6", reasoningEffort: "high", approvalMode: h.approvalMode },
		approvalModes: h.approvalModes,
		chooseTurnSettings: vi.fn(),
		agent: { value: h.harness },
		model: {},
		effort: {},
		showEffort: false,
		renderAgentControl: () => null,
		renderModelControl: () => null,
		renderEffortControl: () => null,
	}),
}));
vi.mock("../hooks/useCloudOffering", () => ({ useCloudOffering: () => h.offering }));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: h.workspaces }),
	useRemoteWorkspaces: () => ({ data: [] }),
	workspaceQueryKeyForHost: () => ["workspaces"],
}));

vi.mock("../hooks/usePlans", async (importOriginal) => ({
	...(await importOriginal<typeof import("../hooks/usePlans")>()),
	createPlan: h.createPlan,
}));

function renderPage(projectId = "proj-1") {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={queryClient}>
			<TooltipProvider>
				<NewChatPage projectId={projectId} />
			</TooltipProvider>
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	window.localStorage.removeItem("ao.newTask.mode.proj-1");
	window.localStorage.removeItem("ao.newTask.pendingLaunch.v1");
	h.navigate.mockReset();
	h.createPlan.mockReset();
	h.isCloudProject = false;
	h.workspace = undefined;
});

describe("New Task plan mode", () => {
	it("starts as a Task and switches to a plan form", async () => {
		renderPage();
		expect(h.workspace).toBeDefined();
		const plan = screen.getByRole("tab", { name: "Plan" });
		expect(screen.getByRole("tab", { name: "Task" })).toHaveAttribute("aria-selected", "true");
		h.workspace = undefined;
		await userEvent.click(plan);
		expect(screen.getByRole("tab", { name: "Plan" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByLabelText("Title")).toBeInTheDocument();
		expect(screen.getByLabelText("Ticket link")).toBeInTheDocument();
		expect(screen.getByLabelText("Ticket link")).toHaveAttribute("placeholder");
		expect(screen.getByRole("textbox", { name: "Brief" })).toBeInTheDocument();
		// No agent, model, mode, or approval pickers: the orchestrator plans.
		expect(h.workspace).toBeUndefined();
		expect(screen.getByRole("button", { name: "Add attachment" })).toBeDisabled();
		expect(window.localStorage.getItem("ao.newTask.mode.proj-1")).toBe("plan");
	});

	it("posts the plan and opens its run view", async () => {
		h.createPlan.mockResolvedValue({ id: "plan_9", projectId: "proj-1" });
		renderPage();
		await userEvent.click(screen.getByRole("tab", { name: "Plan" }));
		const submit = screen.getByRole("button", { name: "Plan with orchestrator" });
		expect(submit).toBeDisabled();
		await userEvent.type(screen.getByLabelText("Title"), "TODO app MVP");
		expect(submit).toBeDisabled();
		await userEvent.type(screen.getByRole("textbox", { name: "Brief" }), "Build a TODO web app");
		await userEvent.type(screen.getByLabelText("Ticket link"), "github:o/r#12");
		await userEvent.click(submit);
		await waitFor(() => expect(h.createPlan).toHaveBeenCalledWith("proj-1", { title: "TODO app MVP", brief: "Build a TODO web app", ticketRef: "github:o/r#12" }));
		await waitFor(() => expect(h.navigate).toHaveBeenCalledWith({ to: "/projects/$projectId/plans/$planId", params: { projectId: "proj-1", planId: "plan_9" } }));
	});

	it("shows the daemon's refusal next to the action", async () => {
		h.createPlan.mockRejectedValue({ error: "bad_request", code: "PLAN_PROJECT_KIND_UNSUPPORTED", message: "Plans need a single-repository project" });
		renderPage();
		await userEvent.click(screen.getByRole("tab", { name: "Plan" }));
		await userEvent.type(screen.getByLabelText("Title"), "x");
		await userEvent.type(screen.getByRole("textbox", { name: "Brief" }), "y");
		await userEvent.click(screen.getByRole("button", { name: "Plan with orchestrator" }));
		expect(await screen.findByRole("alert")).toHaveTextContent("Plans need a single-repository project");
		expect(h.navigate).not.toHaveBeenCalled();
	});

	it("remembers the last mode per project", () => {
		window.localStorage.setItem("ao.newTask.mode.proj-1", "plan");
		renderPage();
		expect(screen.getByRole("tab", { name: "Plan" })).toHaveAttribute("aria-selected", "true");
		expect(screen.getByLabelText("Title")).toBeInTheDocument();
	});

	it("offers plans only for local projects", () => {
		h.isCloudProject = true;
		renderPage();
		expect(screen.queryByRole("tab", { name: "Plan" })).toBeNull();
	});
});
