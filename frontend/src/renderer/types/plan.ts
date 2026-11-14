import type { components } from "../../api/schema";

// Task plans as the daemon serves them; the board calls a plan a Task.
export type PlanSummary = components["schemas"]["PlanSummary"];
export type Plan = components["schemas"]["PlanResponse"];
export type PlanMilestone = components["schemas"]["MilestoneResponse"];
export type PlanEvent = components["schemas"]["PlanEventResponse"];
export type PlanDetail = components["schemas"]["PlanDetailResponse"];
export type PlanSessionSummary = components["schemas"]["PlanSessionSummary"];
export type PlanPhase = PlanSummary["phase"];
export type MilestoneStatus = PlanMilestone["status"];
export type CreatePlanInput = components["schemas"]["CreatePlanRequest"];
