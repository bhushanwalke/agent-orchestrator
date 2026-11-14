// Layered layout for a Task's milestone graph (spec §11.3): longest-path
// layering from the sources, layers 220px apart left to right, nodes in a
// layer 96px apart and centered on the tallest layer, QA always last.

export const PLAN_LAYER_GAP = 220;
export const PLAN_NODE_GAP = 96;

export type PlanLayoutInput = {
	key: string;
	kind: "implement" | "qa";
	ordinal: number;
	dependsOn: readonly string[];
};

export type PlanLayoutNode = { key: string; x: number; y: number; layer: number };
export type PlanLayoutEdge = { from: string; to: string };
export type PlanLayout = {
	nodes: PlanLayoutNode[];
	edges: PlanLayoutEdge[];
	/** Number of layers; the graph spans (layers - 1) * PLAN_LAYER_GAP. */
	layers: number;
	/** Most nodes in one layer; the graph spans (rows - 1) * PLAN_NODE_GAP. */
	rows: number;
};

export function layoutPlan(milestones: readonly PlanLayoutInput[]): PlanLayout {
	const byKey = new Map(milestones.map((m) => [m.key, m]));
	const layerOf = new Map<string, number>();
	const visiting = new Set<string>();
	// Depth-first longest path; a cycle (never valid from the daemon) is cut.
	const layer = (key: string): number => {
		const known = layerOf.get(key);
		if (known !== undefined) return known;
		if (visiting.has(key)) return 0;
		visiting.add(key);
		let depth = 0;
		for (const dep of byKey.get(key)?.dependsOn ?? []) {
			if (byKey.has(dep)) depth = Math.max(depth, layer(dep) + 1);
		}
		visiting.delete(key);
		layerOf.set(key, depth);
		return depth;
	};
	for (const m of milestones) layer(m.key);

	const implementLayers = milestones.filter((m) => m.kind !== "qa").map((m) => layerOf.get(m.key) ?? 0);
	const qaLayer = implementLayers.length ? Math.max(...implementLayers) + 1 : 0;
	for (const m of milestones) {
		if (m.kind === "qa") layerOf.set(m.key, Math.max(qaLayer, layerOf.get(m.key) ?? 0));
	}

	const columns = new Map<number, PlanLayoutInput[]>();
	for (const m of milestones) {
		const at = layerOf.get(m.key) ?? 0;
		columns.set(at, [...(columns.get(at) ?? []), m]);
	}
	const rows = Math.max(0, ...[...columns.values()].map((column) => column.length));
	const nodes: PlanLayoutNode[] = [];
	for (const [at, column] of [...columns.entries()].sort(([a], [b]) => a - b)) {
		column.sort((a, b) => a.ordinal - b.ordinal || a.key.localeCompare(b.key));
		const offset = ((rows - column.length) * PLAN_NODE_GAP) / 2;
		column.forEach((m, index) => {
			nodes.push({ key: m.key, layer: at, x: at * PLAN_LAYER_GAP, y: offset + index * PLAN_NODE_GAP });
		});
	}
	// Draw only direct dependencies: QA depends on every milestone, and an
	// edge it already reaches through another dependency would cross nodes.
	const reaches = (from: string, to: string, seen = new Set<string>()): boolean => {
		if (seen.has(from)) return false;
		seen.add(from);
		const deps = byKey.get(from)?.dependsOn ?? [];
		return deps.includes(to) || deps.some((dep) => reaches(dep, to, seen));
	};
	const edges: PlanLayoutEdge[] = [];
	for (const m of milestones) {
		const deps = m.dependsOn.filter((dep) => byKey.has(dep));
		for (const dep of deps) {
			if (!deps.some((other) => other !== dep && reaches(other, dep))) edges.push({ from: dep, to: m.key });
		}
	}
	const layers = columns.size ? Math.max(...columns.keys()) + 1 : 0;
	return { nodes, edges, layers, rows };
}

/** Nodes in keyboard order: layer by layer, top to bottom. */
export function planNodeOrder(layout: PlanLayout): string[] {
	return [...layout.nodes].sort((a, b) => a.layer - b.layer || a.y - b.y).map((node) => node.key);
}
