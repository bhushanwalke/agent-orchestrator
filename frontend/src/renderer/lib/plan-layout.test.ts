import { describe, expect, it } from "vitest";
import { layoutPlan, PLAN_LAYER_GAP, PLAN_NODE_GAP, planNodeOrder } from "./plan-layout";

const ms = (key: string, dependsOn: string[] = [], kind: "implement" | "qa" = "implement", ordinal = 0) => ({ key, dependsOn, kind, ordinal });

describe("layoutPlan", () => {
	it("puts a milestone after the longest chain of its dependencies and QA last", () => {
		const layout = layoutPlan([
			ms("m1", [], "implement", 1),
			ms("m2", [], "implement", 2),
			ms("m3", ["m1", "m2"], "implement", 3),
			ms("qa", ["m3"], "qa", 4),
		]);
		const layerOf = Object.fromEntries(layout.nodes.map((node) => [node.key, node.layer]));
		expect(layerOf).toEqual({ m1: 0, m2: 0, m3: 1, qa: 2 });
		expect(layout.layers).toBe(3);
		expect(layout.edges).toEqual([
			{ from: "m1", to: "m3" },
			{ from: "m2", to: "m3" },
			{ from: "m3", to: "qa" },
		]);
	});

	it("spaces layers and nodes and centers a short layer on the tallest", () => {
		const layout = layoutPlan([ms("m1", [], "implement", 1), ms("m2", [], "implement", 2), ms("m3", ["m1"], "implement", 3)]);
		const at = Object.fromEntries(layout.nodes.map((node) => [node.key, node]));
		expect(at.m1).toMatchObject({ x: 0, y: 0 });
		expect(at.m2).toMatchObject({ x: 0, y: PLAN_NODE_GAP });
		expect(at.m3).toMatchObject({ x: PLAN_LAYER_GAP, y: PLAN_NODE_GAP / 2 });
		expect(layout.rows).toBe(2);
	});

	it("keeps QA in a layer of its own even before its dependencies are derived", () => {
		const layout = layoutPlan([ms("m1"), ms("m2"), ms("qa", [], "qa")]);
		expect(layout.nodes.find((node) => node.key === "qa")?.layer).toBe(1);
	});

	it("orders keyboard selection layer by layer", () => {
		const layout = layoutPlan([ms("m3", ["m1"], "implement", 3), ms("m1", [], "implement", 1), ms("m2", [], "implement", 2)]);
		expect(planNodeOrder(layout)).toEqual(["m1", "m2", "m3"]);
	});
});
