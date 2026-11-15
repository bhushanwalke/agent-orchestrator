package claudecode

import (
	"context"
	"path/filepath"
	"regexp"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

var cloudNamePart = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// claudeModelRoute identifies the route Claude Code sends model requests over
// when it is not the first-party API: a cloud provider selected by its switch
// (from the environment, the user's settings or the CLI's apiProvider) or an
// ANTHROPIC_BASE_URL gateway. ok is false for first party.
func claudeModelRoute(ctx context.Context, resolved claudeProviderContext, workingDir string, env map[string]string) (domain.AgentModelRoute, bool) {
	routeEnv := agentcreds.ResolveClaudeRouteEnv(ctx, agentcreds.ResolveOptions{WorkingDir: workingDir, CommandEnv: env})
	source := func(key string) string {
		if routeEnv.FromSettings(key) {
			return authprobe.FileSource(routeEnv.SettingsPath).Source
		}
		return key
	}
	provider := ""
	if resolved.providerOK {
		switch resolved.provider {
		case agentcreds.ProviderBedrock, agentcreds.ProviderVertex, agentcreds.ProviderFoundry:
			provider = string(resolved.provider)
		}
	}
	if provider == "" {
		var switched []string
		for _, candidate := range []struct{ key, provider string }{
			{"CLAUDE_CODE_USE_BEDROCK", string(agentcreds.ProviderBedrock)},
			{"CLAUDE_CODE_USE_VERTEX", string(agentcreds.ProviderVertex)},
			{"CLAUDE_CODE_USE_FOUNDRY", string(agentcreds.ProviderFoundry)},
		} {
			if routeEnv.Truthy(candidate.key) {
				switched = append(switched, candidate.provider)
			}
		}
		if len(switched) == 1 {
			provider = switched[0]
		}
	}
	if provider == "" && resolved.cliOK {
		if reported, ok := agentcreds.ParseProvider(resolved.report.APIProvider); ok {
			switch reported {
			case agentcreds.ProviderBedrock, agentcreds.ProviderVertex, agentcreds.ProviderFoundry:
				provider = string(reported)
			}
		}
	}
	route := domain.AgentModelRoute{Kind: domain.AgentModelRouteCloud, Credential: domain.AgentModelRouteCredentialCloudCredentials}
	switch agentcreds.Provider(provider) {
	case agentcreds.ProviderBedrock:
		route.APIFamily, route.ConfigSource = domain.AgentModelRouteFamilyBedrock, source("CLAUDE_CODE_USE_BEDROCK")
		route.Host = domain.RouteHost(routeEnv.Get("ANTHROPIC_BEDROCK_BASE_URL"))
		if route.Host == "" {
			region := routeEnv.Get("AWS_REGION")
			if region == "" {
				region = routeEnv.Get("AWS_DEFAULT_REGION")
			}
			if cloudNamePart.MatchString(region) {
				route.Host = "bedrock-runtime." + region + ".amazonaws.com"
			}
		}
		switch {
		case routeEnv.Truthy("CLAUDE_CODE_SKIP_BEDROCK_AUTH"):
			route.Credential = domain.AgentModelRouteCredentialNone
		case routeEnv.Get("AWS_BEARER_TOKEN_BEDROCK") != "":
			route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialToken, "AWS_BEARER_TOKEN_BEDROCK"
		case routeEnv.Get("AWS_PROFILE") != "":
			route.CredentialSource = "AWS_PROFILE"
		}
		return route, true
	case agentcreds.ProviderVertex:
		route.APIFamily, route.ConfigSource = domain.AgentModelRouteFamilyVertex, source("CLAUDE_CODE_USE_VERTEX")
		route.Host = domain.RouteHost(routeEnv.Get("ANTHROPIC_VERTEX_BASE_URL"))
		if region := routeEnv.Get("CLOUD_ML_REGION"); route.Host == "" && region == "global" {
			route.Host = "aiplatform.googleapis.com"
		} else if route.Host == "" && cloudNamePart.MatchString(region) {
			route.Host = region + "-aiplatform.googleapis.com"
		}
		if routeEnv.Truthy("CLAUDE_CODE_SKIP_VERTEX_AUTH") {
			route.Credential = domain.AgentModelRouteCredentialNone
		}
		return route, true
	case agentcreds.ProviderFoundry:
		route.APIFamily, route.ConfigSource = domain.AgentModelRouteFamilyFoundry, source("CLAUDE_CODE_USE_FOUNDRY")
		route.Host = domain.RouteHost(routeEnv.Get("ANTHROPIC_FOUNDRY_BASE_URL"))
		if resource := routeEnv.Get("ANTHROPIC_FOUNDRY_RESOURCE"); route.Host == "" && cloudNamePart.MatchString(resource) {
			route.Host = resource + ".services.ai.azure.com"
		}
		switch {
		case routeEnv.Truthy("CLAUDE_CODE_SKIP_FOUNDRY_AUTH"):
			route.Credential = domain.AgentModelRouteCredentialNone
		case routeEnv.Get("ANTHROPIC_FOUNDRY_API_KEY") != "":
			route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialAPIKey, "ANTHROPIC_FOUNDRY_API_KEY"
		}
		return route, true
	}
	baseURL := resolved.opts.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		return domain.AgentModelRoute{}, false
	}
	route = domain.AgentModelRoute{
		APIFamily:    domain.AgentModelRouteFamilyAnthropic,
		Host:         domain.RouteHost(baseURL),
		Credential:   domain.AgentModelRouteCredentialNone,
		ConfigSource: source("ANTHROPIC_BASE_URL"),
	}
	if routeEnv.Get("ANTHROPIC_BASE_URL") != baseURL {
		// Only the project's own settings can set a gateway the user
		// configuration does not; the resolver read them for this launch.
		route.ConfigSource = filepath.Join(".claude", "settings.json")
	}
	route.Kind = modelroute.Kind(route.Host)
	switch {
	case resolved.found:
		route.Credential = domain.AgentModelRouteCredentialAccount
		switch resolved.credential.Kind {
		case agentcreds.KindAPIKey:
			route.Credential = domain.AgentModelRouteCredentialAPIKey
		case agentcreds.KindOAuthToken, agentcreds.KindAuthToken:
			route.Credential = domain.AgentModelRouteCredentialToken
		}
		route.CredentialSource = credentialEvidence(resolved.credential).Source
	case routeEnv.APIKeyHelper:
		// Claude Code gets its key from a command AO never runs.
		route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialUnknown, "apiKeyHelper"
	}
	return route, true
}

// routeVerificationFromStep maps the existing provider validator's outcome
// onto the gateway route it checked, so a keyed gateway is not asked twice.
func routeVerificationFromStep(code string) domain.AgentModelRouteVerification {
	switch code {
	case "provider_accepted":
		return domain.AgentModelRouteVerified
	case "provider_rejected":
		return domain.AgentModelRouteRejected
	case "provider_unreachable", "probe_timeout":
		return domain.AgentModelRouteUnreachable
	case "provider_inconclusive", "probe_cancelled":
		return domain.AgentModelRouteInconclusive
	default:
		return domain.AgentModelRouteNotAttempted
	}
}

// checkModelRoute records route with what can be known about it without a
// second request for a keyed gateway, and checks a keyless gateway's model
// list. decided is true when that listing settles the verdict.
func checkModelRoute(ctx context.Context, route domain.AgentModelRoute, baseURL string, validator domain.AgentAuthDiagnostic) (ports.AgentAuthStatus, bool) {
	switch {
	case route.Kind == domain.AgentModelRouteCloud:
		modelroute.Record(ctx, route, modelroute.Result{Verification: domain.AgentModelRouteUnverifiable})
	case route.Credential == domain.AgentModelRouteCredentialNone:
		result := modelroute.Check(ctx, modelroute.Request{BaseURL: baseURL, Family: route.APIFamily})
		route = modelroute.Record(ctx, route, result)
		if route.Verification == domain.AgentModelRouteVerified || route.Verification == domain.AgentModelRouteRejected {
			return modelroute.Decide(ctx, ports.AgentAuthStatusUnknown, []domain.AgentModelRoute{route}), true
		}
	case route.Credential == domain.AgentModelRouteCredentialUnknown:
		modelroute.Record(ctx, route, modelroute.Result{Verification: domain.AgentModelRouteNotAttempted})
	default:
		modelroute.Record(ctx, route, modelroute.Result{
			Verification: routeVerificationFromStep(validator.Code), HTTPStatus: validator.HTTPStatus,
			Cached: validator.Cached, CheckedAt: validator.CheckedAt, Duration: time.Duration(validator.DurationMs) * time.Millisecond,
		})
	}
	return ports.AgentAuthStatusUnknown, false
}
