import type { TFunction } from "i18next";
import { describe, expect, it } from "vitest";
import type { AgentReadinessSnapshot } from "../../../hooks/useAgentReadinessQuery";
import type { AgentId } from "../../../lib/agent-options";
import { agentReadiness } from "../../../test/agent-readiness-fixtures";
import en from "../../../i18n/en.json";
import { describeHarness } from "./describe-harness";
import { healthText } from "./harness-diagnostics";
import { describeRoute, describeRoutes, routeReportLines, routesBypassAccounts, routeStatusLabel, type AgentModelRoute } from "./harness-routes";

describe("configured harness authentication summary", () => {
	it.each([
		["mimo-code", undefined, undefined], // MiMo Auto needs no credentials.
		["claude-code", undefined, undefined],
		["codewhale", undefined, undefined],
		["memcode", undefined, undefined],
		["unreal-agent", undefined, undefined],
		["mimo-code", "credential_detection", "probe_timeout"],
		["hermes", "provider_validation", "provider_accepted"],
	])("uses a neutral label for %s with %s/%s", (agentId, method, code) => {
		expect(summary(agentId as AgentId, method, code)).toBe("settings.harness.installed");
	});

	it("keeps a renewable Claude token connected", () => {
		expect(summary("claude-code", "provider_validation", "token_renewal_pending")).toBe("settings.harness.loggedIn");
	});

	it("identifies unresolved native model selections as unverified", () => {
		expect(summary("mimo-code", "model_selection", "model_selection_unresolved")).toBe("settings.harness.health.authUnverified");
	});

	it.each(["mimo-code", "hermes"] as const)("identifies inconclusive provider checks for %s", (agentId) => {
		for (const code of ["provider_unsupported", "provider_inconclusive", "provider_unreachable", "probe_timeout", "probe_cancelled"]) {
			expect(summary(agentId, "provider_validation", code)).toBe("settings.harness.health.authUnverified");
		}
	});
});

function summary(agentId: AgentId, method?: string, code?: string) {
	const readinessAgent = agentReadiness(agentId, agentId, { authentication: "configured" });
	if (method && code) readinessAgent.authentication.diagnostics = [{
		method, code, reason: "Test observation", checkedAt: "2026-10-10T10:00:00Z", cached: false, durationMs: 0,
	}];
	return describeHarness({
		agentId, readinessAgent, t: ((key: string) => key) as TFunction, language: "en",
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null,
	}).authSummary;
}

// Interpolates the English catalog, so route text is checked as users read it.
const english = ((key: string, values?: Record<string, unknown>) => {
	const text = String((en as Record<string, unknown>)[key] ?? key);
	return text.replace(/\{\{(\w+)\}\}/g, (_, name: string) => String(values?.[name] ?? ""));
}) as TFunction;

const route = (overrides: Partial<AgentModelRoute> = {}): AgentModelRoute => ({
	kind: "gateway", apiFamily: "openai", host: "gw.example.com", credential: "none", verification: "verified", ...overrides,
});

function routedHarness(state: "configured" | "authorized" | "unknown", routes: AgentModelRoute[], account?: AgentReadinessSnapshot["authentication"]["account"]) {
	const readinessAgent = agentReadiness("codex", "Codex", { authentication: state });
	readinessAgent.authentication.routes = routes;
	readinessAgent.authentication.account = account;
	return describeHarness({
		agentId: "codex", readinessAgent, t: english, language: "en",
		installed: true, pending: false, readinessPending: false, readinessFailed: false,
		authPlansPending: false, authPlansFailed: false, authWorkflow: null,
	});
}

describe("model routes", () => {
	it("describes one route", () => {
		expect(describeRoute(route({ kind: "cloud", apiFamily: "bedrock" }), english)).toBe("AWS Bedrock via gw.example.com · no key (network access)");
		expect(describeRoute(route({ name: "fp-gw", credential: "api_key" }), english)).toBe("Provider fp-gw · OpenAI API via gw.example.com");
		expect(describeRoute(route({ host: undefined, apiFamily: "vertex", credential: "cloud_credentials" }), english)).toBe("Google Vertex AI");
	});

	it("summarizes several providers", () => {
		expect(describeRoutes([], english)).toBeUndefined();
		expect(describeRoutes([route(), route({ name: "b" }), route({ name: "c", verification: "inconclusive" })], english)).toBe("3 providers via gw.example.com · 2 verified");
		expect(describeRoutes([route({ verification: "unreachable" }), route({ host: "other.example.com", verification: "unreachable" })], english)).toBe("2 providers");
	});

	it("labels a configured harness by what its routes found", () => {
		expect(routeStatusLabel("configured", [route({ verification: "unverifiable" })], english)).toBe("Configured, can't be verified");
		expect(routeStatusLabel("configured", [route({ verification: "access_challenge" }), route({ verification: "unreachable" })], english)).toBe("Needs network access");
		expect(routeStatusLabel("configured", [route({ verification: "unreachable" })], english)).toBe("Can't reach provider");
		expect(routeStatusLabel("configured", [route({ verification: "verified" })], english)).toBeUndefined();
		expect(routeStatusLabel("authorized", [route({ verification: "unverifiable" })], english)).toBeUndefined();
		expect(routeStatusLabel("configured", [], english)).toBeUndefined();
	});

	it("shows the route on the harness row and in its status", () => {
		const configured = routedHarness("configured", [route({ kind: "cloud", apiFamily: "bedrock", verification: "unverifiable" })]);
		expect(configured.authSummary).toBe("Configured, can't be verified");
		expect(configured.accountDescription).toBe("AWS Bedrock via gw.example.com · no key (network access)");
		const unrouted = routedHarness("configured", []);
		expect(unrouted.routes).toEqual([]);
		expect(unrouted.routeDescription).toBeUndefined();
		// A route on an unknown verdict is still shown.
		expect(routedHarness("unknown", [route({ verification: "not_attempted", credential: "api_key" })]).accountDescription).toBe("OpenAI API via gw.example.com");
	});

	it("does not repeat a route's provider as a bare name on the account line", () => {
		const routes = [route({ name: "gw-keyless" }), route({ name: "b" }), route({ name: "c", verification: "inconclusive" })];
		expect(routedHarness("authorized", routes, { provider: "gw-keyless" }).accountDescription).toBe("3 providers via gw.example.com · 2 verified");
		expect(routedHarness("authorized", [route({ name: "fp-gw" })], { provider: "fp-gw", method: "api_key" }).accountDescription).toBe("Using an API key · Provider fp-gw · OpenAI API via gw.example.com · no key (network access)");
		expect(routedHarness("authorized", [route({ name: "gw" })], { provider: "zai-coding-plan" }).accountDescription).toContain("zai-coding-plan · ");
	});

	it("says when subscriptions do not reach the route", () => {
		expect(routesBypassAccounts([])).toBe(false);
		expect(routesBypassAccounts([route({ kind: "cloud", credential: "cloud_credentials" })])).toBe(true);
		expect(routesBypassAccounts([route({ credential: "none" }), route({ credential: "api_key" })])).toBe(true);
		expect(routesBypassAccounts([route({ credential: "account" })])).toBe(false);
		expect(routesBypassAccounts([route({ credential: "unknown" })])).toBe(false);
	});

	it("puts every route in the copied health report", () => {
		const lines = routeReportLines([route({ name: "fp-gw", credential: "api_key", credentialSource: "FP_KEY", configSource: "~/.codex/config.toml", verification: "rejected", httpStatus: 401, isDefault: true })]);
		expect(lines).toEqual(["Model route: fp-gw · gateway · openai via gw.example.com (default)\nCredential: api_key (FP_KEY)\nConfigured in: ~/.codex/config.toml\nVerification: rejected (HTTP 401)"]);
		const report = healthText(routedHarness("configured", [route({ verification: "access_challenge" })]), { t: english });
		expect(report).toContain("Model route: gateway · openai via gw.example.com");
		expect(report).toContain("Verification: access_challenge");
	});
});
