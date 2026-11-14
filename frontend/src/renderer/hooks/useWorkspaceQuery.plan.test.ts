import { describe, expect, it } from "vitest";
import type { components } from "../../api/schema";
import { toWorkspaceSession } from "./useWorkspaceQuery";

const view = (overrides: Partial<components["schemas"]["ControllersSessionView"]> = {}) =>
	({ id: "w-1", projectId: "p1", status: "working", harness: "claude-code", kind: "worker", ...overrides }) as components["schemas"]["ControllersSessionView"];

describe("toWorkspaceSession plan fields", () => {
	it("keeps the Task a project session works for, so the board can fold it", () => {
		const session = toWorkspaceSession(view({ planId: "plan_1", planMilestoneId: "ms_1", sparsePaths: ["web"] }), { id: "p1", name: "radic" });
		expect(session).toMatchObject({ planId: "plan_1", planMilestoneId: "ms_1", sparsePaths: ["web"] });
		expect(toWorkspaceSession(view(), { id: "p1", name: "radic" }).planId).toBeUndefined();
	});
});
