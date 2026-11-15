import type { TFunction } from "i18next";
import type { components } from "../../../../api/schema";

export type AgentModelRoute = components["schemas"]["AgentModelRoute"];
type AuthenticationState = components["schemas"]["AgentAuthenticationObservation"]["state"];

/** One route: "AWS Bedrock via gw.example.com", named and keyless when it is. */
export function describeRoute(route: AgentModelRoute, t: TFunction): string {
	const family = t(`settings.harness.route.family.${route.apiFamily}`);
	let text = route.host ? t("settings.harness.route.via", { family, host: route.host }) : family;
	if (route.name) text = t("settings.harness.route.named", { name: route.name, route: text });
	if (route.credential === "none") text = `${text} · ${t("settings.harness.route.noKey")}`;
	return text;
}

/** The route line for the harness row: one route, or how many providers and how many verified. */
export function describeRoutes(routes: readonly AgentModelRoute[], t: TFunction): string | undefined {
	if (routes.length === 0) return undefined;
	if (routes.length === 1) return describeRoute(routes[0], t);
	const host = routes[0].host;
	const providers = host && routes.every((route) => route.host === host)
		? t("settings.harness.route.several", { count: routes.length, host })
		: t("settings.harness.route.severalHosts", { count: routes.length });
	const verified = routes.filter((route) => route.verification === "verified").length;
	return verified > 0 ? `${providers} · ${t("settings.harness.route.verifiedCount", { count: verified })}` : providers;
}

/** Why a configured harness could not be verified, when its routes say so.
	* Only for configured: a verified route already reads as logged in. */
export function routeStatusLabel(state: AuthenticationState | undefined, routes: readonly AgentModelRoute[], t: TFunction): string | undefined {
	if (state !== "configured" || routes.length === 0 || routes.some((route) => route.verification === "verified")) return undefined;
	if (routes.some((route) => route.verification === "access_challenge")) return t("settings.harness.route.needsNetwork");
	if (routes.every((route) => route.verification === "unreachable")) return t("settings.harness.route.unreachable");
	return t("settings.harness.route.unverifiable");
}

export function routeVerificationLabel(route: AgentModelRoute, t: TFunction): string {
	return t(`settings.harness.route.verification.${route.verification}`);
}

/** True when the subscriptions a harness lists do not reach its model requests:
	* every route is a cloud provider or carries its own key, or none at all. */
export function routesBypassAccounts(routes: readonly AgentModelRoute[]): boolean {
	return routes.length > 0 && routes.every((route) => route.kind === "cloud" || !["token", "account", "unknown"].includes(route.credential));
}

/** English lines for the copied health report, one block per route. */
export function routeReportLines(routes: readonly AgentModelRoute[]): string[] {
	return routes.map((route) => [
		`Model route: ${route.name ? `${route.name} · ` : ""}${route.kind} · ${route.apiFamily}${route.host ? ` via ${route.host}` : ""}${route.isDefault ? " (default)" : ""}`,
		`Credential: ${route.credential}${route.credentialSource ? ` (${route.credentialSource})` : ""}`,
		route.configSource ? `Configured in: ${route.configSource}` : "",
		`Verification: ${route.verification}${route.httpStatus ? ` (HTTP ${route.httpStatus})` : ""}`,
	].filter(Boolean).join("\n"));
}
