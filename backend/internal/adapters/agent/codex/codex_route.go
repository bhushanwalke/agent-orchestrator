package codex

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// codexLocalProviderURLs are Codex's built-in local providers, used when the
// config names one without a base_url.
var codexLocalProviderURLs = map[string]string{
	"oss":      "http://localhost:11434/v1",
	"ollama":   "http://localhost:11434/v1",
	"lmstudio": "http://localhost:1234/v1",
}

// customProviderStatus checks a custom model provider by listing its models
// with exactly the headers Codex sends it: Authorization from env_key, plus
// http_headers and env_http_headers. Codex's own login does not apply.
func customProviderStatus(ctx context.Context, provider codexModelProvider) ports.AgentAuthStatus {
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		baseURL = codexLocalProviderURLs[provider.Name]
	}
	route := domain.AgentModelRoute{
		Name:         provider.Name,
		APIFamily:    domain.AgentModelRouteFamilyOpenAICompatible,
		Host:         domain.RouteHost(baseURL),
		Credential:   domain.AgentModelRouteCredentialNone,
		ConfigSource: authprobe.FileSource(filepath.Join(codexHomeDir(), "config.toml")).Source,
	}
	if strings.EqualFold(strings.TrimSpace(provider.WireAPI), "responses") {
		route.APIFamily = domain.AgentModelRouteFamilyOpenAI
	}
	route.Kind = modelroute.Kind(route.Host)

	headers := http.Header{}
	missing := false
	for name, value := range provider.HTTPHeaders {
		headers.Set(name, value)
		if isAuthHeader(name) {
			route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialToken, route.ConfigSource
		}
	}
	for name, variable := range provider.EnvHTTPHeaders {
		value := strings.TrimSpace(authprobe.Getenv(ctx, variable))
		if value != "" {
			headers.Set(name, value)
		}
		if isAuthHeader(name) {
			route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialToken, variable
			missing = missing || value == ""
		}
	}
	keyPresent := false
	if provider.EnvKey != "" {
		route.Credential, route.CredentialSource = domain.AgentModelRouteCredentialAPIKey, provider.EnvKey
		// Records the variable as evidence, or that it is missing, as before.
		if authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, provider.EnvKey) != "" {
			keyPresent = true
			headers.Set("Authorization", "Bearer "+strings.TrimSpace(authprobe.Getenv(ctx, provider.EnvKey)))
		} else {
			missing = true
		}
	}
	if missing {
		modelroute.RecordMissingCredential(ctx, route)
		return ports.AgentAuthStatusUnknown
	}
	if route.Host == "" {
		modelroute.Record(ctx, route, modelroute.Result{Verification: domain.AgentModelRouteInconclusive})
		return legacyCustomProviderStatus(keyPresent)
	}
	route = modelroute.Record(ctx, route, modelroute.Check(ctx, modelroute.Request{BaseURL: baseURL, Family: route.APIFamily, Headers: headers}))
	return modelroute.Decide(ctx, ports.AgentAuthStatusUnknown, []domain.AgentModelRoute{route})
}

// legacyCustomProviderStatus is the verdict before routes were checked: a
// present key is configured, anything else unknown.
func legacyCustomProviderStatus(keyPresent bool) ports.AgentAuthStatus {
	if keyPresent {
		return ports.AgentAuthStatusConfigured
	}
	return ports.AgentAuthStatusUnknown
}

func isAuthHeader(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return name == "authorization" || strings.Contains(name, "api-key") || strings.Contains(name, "token")
}
