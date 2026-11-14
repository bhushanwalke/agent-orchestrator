import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import type { CreatePlanInput, Plan, PlanDetail, PlanEvent, PlanSummary } from "../types/plan";

// Task plans are local-project only, so their keys carry no host.
export const plansQueryRoot = ["plans"] as const;
export const planQueryRoot = ["plan"] as const;
export const plansQueryKey = (projectId: string) => ["plans", projectId] as const;
export const planQueryKey = (planId: string) => ["plan", planId] as const;
export const planEventsQueryKey = (planId: string) => ["plan", planId, "events"] as const;

export async function fetchPlans(projectId: string): Promise<PlanSummary[]> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/plans", {
		params: { path: { projectId } },
	});
	if (error) throw error;
	return data?.plans ?? [];
}

export async function fetchPlan(planId: string): Promise<PlanDetail> {
	const { data, error } = await apiClient.GET("/api/v1/plans/{planId}", { params: { path: { planId } } });
	if (error) throw error;
	if (!data) throw new Error("empty plan response");
	return data;
}

/** A project's Tasks, newest first. Disabled for cloud and remote boards. */
export function usePlansQuery(projectId: string | undefined, enabled = true) {
	return useQuery({
		queryKey: plansQueryKey(projectId ?? ""),
		queryFn: () => fetchPlans(projectId ?? ""),
		enabled: Boolean(projectId) && enabled,
		retry: 1,
	});
}

/** One Task with its milestones and its latest 100 events, newest first. */
export function usePlanQuery(planId: string | undefined) {
	return useQuery({
		queryKey: planQueryKey(planId ?? ""),
		queryFn: () => fetchPlan(planId ?? ""),
		enabled: Boolean(planId),
		retry: 1,
	});
}

/** A Task's whole activity log, oldest first (up to 500 entries). */
export function usePlanEventsQuery(planId: string | undefined, enabled = true) {
	return useQuery({
		queryKey: planEventsQueryKey(planId ?? ""),
		queryFn: async (): Promise<PlanEvent[]> => {
			const { data, error } = await apiClient.GET("/api/v1/plans/{planId}/events", {
				params: { path: { planId: planId ?? "" }, query: { limit: 500 } },
			});
			if (error) throw error;
			return data?.events ?? [];
		},
		enabled: Boolean(planId) && enabled,
	});
}

export async function createPlan(projectId: string, input: CreatePlanInput): Promise<Plan> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/plans", {
		params: { path: { projectId } },
		body: input,
	});
	if (error) throw error;
	if (!data) throw new Error("empty plan response");
	return data.plan;
}

/** Cancel, retry, and edit actions on one Task; each refreshes its views. */
export function usePlanMutations(planId: string, projectId: string) {
	const queryClient = useQueryClient();
	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: planQueryKey(planId) });
		void queryClient.invalidateQueries({ queryKey: plansQueryKey(projectId) });
	};
	const cancel = useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/plans/{planId}/cancel", { params: { path: { planId } } });
			if (error) throw error;
		},
		onSettled: refresh,
	});
	const retryMilestone = useMutation({
		mutationFn: async (key: string) => {
			const { error } = await apiClient.POST("/api/v1/plans/{planId}/milestones/{key}/retry", {
				params: { path: { planId, key } },
			});
			if (error) throw error;
		},
		onSettled: refresh,
	});
	const patch = useMutation({
		mutationFn: async (body: { title?: string; maxParallel?: number; specMarkdown?: string }) => {
			const { error } = await apiClient.PATCH("/api/v1/plans/{planId}", { params: { path: { planId } }, body });
			if (error) throw error;
		},
		onSettled: refresh,
	});
	return { cancel, retryMilestone, patch };
}
