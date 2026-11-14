import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: h.get, POST: h.post, PATCH: h.patch },
}));

import { planQueryKey, plansQueryKey, usePlanMutations, usePlanQuery, usePlansQuery } from "./usePlans";

function wrapper(queryClient: QueryClient) {
	return ({ children }: { children: ReactNode }) => <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

describe("usePlans", () => {
	beforeEach(() => {
		h.get.mockReset();
		h.post.mockReset();
		h.patch.mockReset();
	});

	it("keys project plans and one plan separately", () => {
		expect(plansQueryKey("mer")).toEqual(["plans", "mer"]);
		expect(planQueryKey("plan_1")).toEqual(["plan", "plan_1"]);
	});

	it("reads a project's plans and one plan detail", async () => {
		h.get.mockImplementation(async (path: string) =>
			path === "/api/v1/projects/{projectId}/plans"
				? { data: { plans: [{ id: "plan_1" }] } }
				: { data: { plan: { id: "plan_1" }, milestones: [], events: [] } },
		);
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const list = renderHook(() => usePlansQuery("mer"), { wrapper: wrapper(client) });
		await waitFor(() => expect(list.result.current.data).toEqual([{ id: "plan_1" }]));
		expect(h.get).toHaveBeenCalledWith("/api/v1/projects/{projectId}/plans", { params: { path: { projectId: "mer" } } });
		const detail = renderHook(() => usePlanQuery("plan_1"), { wrapper: wrapper(client) });
		await waitFor(() => expect(detail.result.current.data?.plan.id).toBe("plan_1"));
		const none = renderHook(() => usePlansQuery(undefined), { wrapper: wrapper(client) });
		expect(none.result.current.fetchStatus).toBe("idle");
	});

	it("refreshes the plan and the board after a mutation", async () => {
		h.post.mockResolvedValue({ data: {} });
		const client = new QueryClient();
		const spy = vi.spyOn(client, "invalidateQueries");
		const { result } = renderHook(() => usePlanMutations("plan_1", "mer"), { wrapper: wrapper(client) });
		await act(async () => {
			await result.current.retryMilestone.mutateAsync("qa");
		});
		expect(h.post).toHaveBeenCalledWith("/api/v1/plans/{planId}/milestones/{key}/retry", { params: { path: { planId: "plan_1", key: "qa" } } });
		expect(spy).toHaveBeenCalledWith({ queryKey: ["plan", "plan_1"] });
		expect(spy).toHaveBeenCalledWith({ queryKey: ["plans", "mer"] });
	});
});
