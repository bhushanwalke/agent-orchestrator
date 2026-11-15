package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func routeConfigFixture(t *testing.T, files map[string]string) {
	t.Helper()
	home := t.TempDir()
	previous := ConfigHome
	ConfigHome = func() string { return home }
	t.Cleanup(func() { ConfigHome = previous })
	for name, body := range files {
		path := filepath.Join(home, "opencode", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT", "AO_MODEL_ROUTE_PROBE"} {
		t.Setenv(name, "")
	}
	modelroute.InvalidateCache()
	t.Cleanup(modelroute.InvalidateCache)
}

func routeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previous := modelroute.Client
	modelroute.Client = server.Client()
	t.Cleanup(func() { modelroute.Client = previous })
	return server
}

func TestProviderRoutesFromJSONCConfig(t *testing.T) {
	var keyed, keyless, anthropic int
	server := routeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/keyless/"):
			keyless++
			if r.Header.Get("Authorization") != "" {
				t.Error("keyless provider sent a key")
			}
		case strings.HasPrefix(r.URL.Path, "/keyed/"):
			keyed++
			if r.Header.Get("Authorization") != "Bearer sk-env" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"key"}`))
				return
			}
		case strings.HasPrefix(r.URL.Path, "/anthropic/"):
			anthropic++
			if r.Header.Get("x-api-key") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"key"}`))
				return
			}
			if r.Header.Get("x-api-key") != "sk-literal" || r.URL.Path != "/anthropic/v1/models" {
				t.Errorf("anthropic request %s with x-api-key %q", r.URL.Path, r.Header.Get("x-api-key"))
			}
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	t.Setenv("FP_KEY", "sk-env")
	routeConfigFixture(t, map[string]string{
		"opencode.json": `{"model":"zz-keyless/m","provider":{"zz-keyless":{"options":{"baseURL":"https://override.example.com"}}}}`,
		"opencode.jsonc": `{
  // later layer overrides the earlier one by id
  "provider": {
    "zz-keyless": {"npm": "@ai-sdk/openai-compatible", "options": {"baseURL": "` + server.URL + `/keyless/v1", "apiKey": ""}},
    "keyed": {"npm": "@ai-sdk/openai", "options": {"baseURL": "` + server.URL + `/keyed/v1", "apiKey": "{env:FP_KEY}"}},
    "lit": {"npm": "@ai-sdk/anthropic", "options": {"baseURL": "` + server.URL + `/anthropic", "apiKey": "sk-literal"}},
    "bedrock": {"npm": "@ai-sdk/amazon-bedrock", "options": {"baseURL": "https://gw.example.com/bedrock"}},
    "stored": {"options": {"baseURL": "https://gw.example.com/stored/v1"}},
    "builtin": {"options": {"timeout": 5}},
  },
}`,
	})
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	routes := CheckProviderRoutes(ctx, map[string]bool{"stored": true})
	names := []string{}
	for _, route := range routes {
		names = append(names, route.Name)
	}
	if strings.Join(names, ",") != "zz-keyless,bedrock,keyed,lit,stored" {
		t.Fatalf("order = %v, want the default provider first, then by id", names)
	}
	byName := map[string]domain.AgentModelRoute{}
	for _, route := range routes {
		byName[route.Name] = route
		if strings.Contains(route.CredentialSource, "sk-") || strings.Contains(route.ConfigSource, "sk-") {
			t.Fatalf("a key value reached a source: %+v", route)
		}
	}
	if r := byName["zz-keyless"]; !r.IsDefault || r.Credential != domain.AgentModelRouteCredentialNone || r.Verification != domain.AgentModelRouteVerified || r.Kind != domain.AgentModelRouteLocal {
		t.Fatalf("keyless = %+v", r)
	}
	if r := byName["keyed"]; r.Credential != domain.AgentModelRouteCredentialAPIKey || r.CredentialSource != "FP_KEY" || r.Verification != domain.AgentModelRouteVerified || r.APIFamily != domain.AgentModelRouteFamilyOpenAI {
		t.Fatalf("keyed = %+v", r)
	}
	if r := byName["lit"]; r.APIFamily != domain.AgentModelRouteFamilyAnthropic || r.Verification != domain.AgentModelRouteVerified {
		t.Fatalf("literal = %+v", r)
	}
	if r := byName["bedrock"]; r.APIFamily != domain.AgentModelRouteFamilyBedrock || r.Verification != domain.AgentModelRouteUnverifiable || r.Host != "gw.example.com" {
		t.Fatalf("bedrock = %+v", r)
	}
	if r := byName["stored"]; r.Credential != domain.AgentModelRouteCredentialUnknown || r.Verification != domain.AgentModelRouteNotAttempted {
		t.Fatalf("stored = %+v", r)
	}
	if keyless != 1 || keyed != 2 || anthropic != 2 {
		t.Fatalf("requests keyless=%d keyed=%d anthropic=%d", keyless, keyed, anthropic)
	}
	if got := evidence.Routes(); len(got) != 5 || got[0].Name != "zz-keyless" {
		t.Fatalf("evidence routes = %+v", got)
	}
	if got := WithRoutes(ctx, ports.AgentAuthStatusUnknown, routes); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("WithRoutes = %s", got)
	}
}

func TestProviderRoutesMissingEnvKeyIsNotChecked(t *testing.T) {
	routeServer(t, func(http.ResponseWriter, *http.Request) { t.Error("checked a provider without its key") })
	t.Setenv("FP_MISSING", "")
	routeConfigFixture(t, map[string]string{
		"opencode.json": `{"provider":{"gw":{"options":{"baseURL":"https://gw.example.com/v1","apiKey":"{env:FP_MISSING}"}}}}`,
	})
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	routes := CheckProviderRoutes(ctx, nil)
	if len(routes) != 1 || routes[0].Verification != domain.AgentModelRouteNotAttempted || routes[0].CredentialSource != "FP_MISSING" {
		t.Fatalf("routes = %+v", routes)
	}
	found := false
	for _, step := range evidence.Diagnostics() {
		found = found || step.Code == "route_credential_missing"
	}
	if !found {
		t.Fatal("no route_credential_missing diagnostic")
	}
	if got := WithRoutes(ctx, ports.AgentAuthStatusUnknown, routes); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("WithRoutes = %s, want configured", got)
	}
}

func TestProviderRoutesReadInlineConfigContent(t *testing.T) {
	routeServer(t, func(http.ResponseWriter, *http.Request) {})
	routeConfigFixture(t, nil)
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"inline":{"npm":"@ai-sdk/google","options":{"baseURL":"https://gw.example.com"}}}}`)
	routes := CheckProviderRoutes(context.Background(), nil)
	if len(routes) != 1 || routes[0].ConfigSource != "OPENCODE_CONFIG_CONTENT" || routes[0].APIFamily != domain.AgentModelRouteFamilyGoogle {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestFamilyForNPM(t *testing.T) {
	cases := map[string]domain.AgentModelRouteFamily{
		"@ai-sdk/openai": domain.AgentModelRouteFamilyOpenAI, "@ai-sdk/openai-compatible": domain.AgentModelRouteFamilyOpenAICompatible,
		"": domain.AgentModelRouteFamilyOpenAICompatible, "@ai-sdk/anthropic": domain.AgentModelRouteFamilyAnthropic,
		"@ai-sdk/amazon-bedrock": domain.AgentModelRouteFamilyBedrock, "@ai-sdk/google-vertex/anthropic": domain.AgentModelRouteFamilyVertex,
		"@ai-sdk/google": domain.AgentModelRouteFamilyGoogle, "@ai-sdk/azure": domain.AgentModelRouteFamilyFoundry, "ollama-ai-provider": domain.AgentModelRouteFamilyOther,
	}
	for npm, want := range cases {
		if got := familyForNPM(npm); got != want {
			t.Errorf("familyForNPM(%q) = %s, want %s", npm, got, want)
		}
	}
}
