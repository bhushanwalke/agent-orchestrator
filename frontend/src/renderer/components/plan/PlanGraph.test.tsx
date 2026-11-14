import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { PlanMilestone } from "../../types/plan";
import { PlanGraph } from "./PlanGraph";

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
		attempt: 0,
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

const graph = [
	milestone("m1", "done"),
	milestone("m2", "running", { session: { id: "w-2", harness: "claude-code", status: "working", displayStatus: "Working", kanbanColumn: "building", isTerminated: false } }),
	milestone("m3", "pending", { dependsOn: ["m1", "m2"] }),
	milestone("qa", "pending", { dependsOn: ["m3"] }),
];

function Harness({ milestones = graph, onOpen = vi.fn() }: { milestones?: PlanMilestone[]; onOpen?: (m: PlanMilestone) => void }) {
	const [selected, setSelected] = useState<string>();
	return <PlanGraph milestones={milestones} onOpen={onOpen} onSelect={setSelected} selectedKey={selected} />;
}

describe("PlanGraph", () => {
	it("draws one node per milestone with its status glyph, and an edge per dependency", () => {
		render(
			<Harness
				milestones={[
					milestone("m1", "done"),
					milestone("m2", "running"),
					milestone("m3", "pending"),
					milestone("m4", "blocked"),
					milestone("m5", "failed"),
					milestone("m6", "cancelled", { dependsOn: ["m1"] }),
				]}
			/>,
		);
		const nodes = screen.getAllByTestId("plan-graph-node");
		expect(nodes.map((node) => node.getAttribute("data-status"))).toEqual(["done", "running", "pending", "blocked", "failed", "cancelled"]);
		for (const status of ["done", "running", "pending", "blocked", "failed", "cancelled"]) {
			expect(screen.getAllByTestId(`plan-glyph-${status}`).length).toBeGreaterThan(0);
		}
		expect(screen.getAllByTestId("plan-graph-edge")).toHaveLength(1);
		expect(screen.getByRole("list", { name: "Milestone states" })).toHaveTextContent(/Done.*Running.*Pending.*Blocked.*Failed/);
	});

	it("says what a pending milestone waits for and which agent runs a running one", () => {
		render(<Harness />);
		expect(screen.getByText("Waiting for m3")).toBeInTheDocument();
		expect(screen.getByText("Waiting for m2")).toBeInTheDocument();
		expect(screen.getByText(/^Claude Code/)).toBeInTheDocument();
	});

	it("moves the selection with arrow keys in layer order and opens a session on Enter", () => {
		const onOpen = vi.fn();
		render(<Harness onOpen={onOpen} />);
		const node = (name: string) => screen.getByRole("button", { name: new RegExp(`^Milestone ${name}:`) });
		fireEvent.click(node("m1"));
		expect(node("m1")).toHaveAttribute("aria-pressed", "true");
		fireEvent.keyDown(node("m1"), { key: "ArrowDown" });
		expect(node("m2")).toHaveAttribute("aria-pressed", "true");
		expect(node("m2")).toHaveFocus();
		fireEvent.keyDown(node("m2"), { key: "ArrowRight" });
		expect(node("m3")).toHaveAttribute("aria-pressed", "true");
		fireEvent.keyDown(node("m3"), { key: "ArrowLeft" });
		expect(node("m2")).toHaveAttribute("aria-pressed", "true");
		fireEvent.keyDown(node("m2"), { key: "Enter" });
		expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ key: "m2" }));
		// A milestone without a session has nothing to open.
		fireEvent.doubleClick(node("m3"));
		expect(onOpen).toHaveBeenCalledTimes(1);
	});
});
