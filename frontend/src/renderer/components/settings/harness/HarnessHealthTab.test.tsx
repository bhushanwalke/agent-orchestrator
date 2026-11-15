import { render, screen } from "@testing-library/react";
import { useTranslation } from "react-i18next";
import { describe, expect, it } from "vitest";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import { TooltipProvider } from "../../ui/tooltip";
import { describeHarness } from "./describe-harness";
import { HarnessHealthTab } from "./HarnessHealthTab";
import type { AgentModelRoute } from "./harness-routes";

const longRoute: AgentModelRoute = {
	name: "a-very-long-provider-identifier-for-a-company-gateway", kind: "gateway", apiFamily: "openai_compatible",
	host: "llm-gateway.internal.example-company-with-a-long-name.com:8443", credential: "none", verification: "unverifiable",
};

function Harness() {
	const { t } = useTranslation();
	const readinessAgent = agentReadiness("opencode-v2", "OpenCode 2", { authentication: "configured" });
	readinessAgent.authentication.routes = [longRoute];
	const details = describeHarness({
		agentId: "opencode-v2", readinessAgent, t, language: "en",
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null,
	});
	return <HarnessHealthTab details={details} checking={false} authCheckFailed={false} copied={false} onCheckAgain={() => {}} onCopyReport={() => {}} />;
}

describe("HarnessHealthTab model routes", () => {
	it("wraps a long route under its label instead of in the value cell", () => {
		render(<TooltipProvider><Harness /></TooltipProvider>);
		const label = screen.getByText("Model route");
		const row = label.parentElement!;
		// The route and its check are the row's description, which wraps at the label's width.
		expect(row.textContent).toContain(longRoute.host);
		expect(row.textContent).toContain("Can't be verified without a model call");
		const value = row.nextElementSibling;
		expect(value?.textContent ?? "").toBe("");
	});
});
