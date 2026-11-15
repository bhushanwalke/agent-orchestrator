package agent

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

var routeFamilyNames = map[domain.AgentModelRouteFamily]string{
	domain.AgentModelRouteFamilyAnthropic:        "Anthropic API",
	domain.AgentModelRouteFamilyOpenAI:           "OpenAI API",
	domain.AgentModelRouteFamilyOpenAICompatible: "an OpenAI-compatible API",
	domain.AgentModelRouteFamilyBedrock:          "AWS Bedrock",
	domain.AgentModelRouteFamilyVertex:           "Google Vertex AI",
	domain.AgentModelRouteFamilyFoundry:          "Azure AI Foundry",
	domain.AgentModelRouteFamilyGoogle:           "Google AI",
}

// describeRoutes names the routes for an authentication reason, in English
// like the rest of the readiness reasons: "AWS Bedrock via gw.example.com
// with no key", or "several providers via gw.example.com".
func describeRoutes(routes []domain.AgentModelRoute) string {
	switch len(routes) {
	case 0:
		return ""
	case 1:
		return describeRoute(routes[0])
	}
	host := routes[0].Host
	for _, route := range routes[1:] {
		if route.Host != host {
			return "several providers"
		}
	}
	if host == "" {
		return "several providers"
	}
	return "several providers via " + host
}

func describeRoute(route domain.AgentModelRoute) string {
	text, ok := routeFamilyNames[route.APIFamily]
	if !ok {
		text = "a custom API"
	}
	if route.Host != "" {
		text += " via " + route.Host
	}
	if route.Name != "" {
		text = "provider " + route.Name + " (" + text + ")"
	}
	if route.Credential == domain.AgentModelRouteCredentialNone {
		text += " with no key"
	}
	return text
}
