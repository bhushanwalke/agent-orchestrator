package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func codexRouteFixture(t *testing.T, config string, handler http.HandlerFunc) (status ports.AgentAuthStatus, routes []domain.AgentModelRoute, steps []domain.AgentAuthDiagnostic, server *httptest.Server) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the codex binary")
	}
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 'Not logged in'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	modelroute.InvalidateCache()
	t.Cleanup(modelroute.InvalidateCache)
	t.Setenv("AO_MODEL_ROUTE_PROBE", "")
	if handler != nil {
		server = httptest.NewServer(handler)
		t.Cleanup(server.Close)
		previous := modelroute.Client
		modelroute.Client = server.Client()
		t.Cleanup(func() { modelroute.Client = previous })
		config = strings.ReplaceAll(config, "{BASE}", server.URL)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, err := (&Plugin{resolvedBinary: binary}).AuthStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return status, evidence.Routes(), evidence.Diagnostics(), server
}

func listing(check func(*http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if check != nil && !check(r) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-5"}]}`))
	}
}

const keylessProvider = "model_provider = \"fp-gw\"\n[model_providers.fp-gw]\nname = \"Gateway\"\nbase_url = \"{BASE}/v1\"\nwire_api = \"responses\"\n"

func TestCustomProviderKeylessListingAuthorizes(t *testing.T) {
	status, routes, steps, _ := codexRouteFixture(t, keylessProvider, listing(func(r *http.Request) bool {
		if r.Header.Get("Authorization") != "" {
			t.Error("a keyless provider sent a credential")
		}
		return r.URL.Path == "/v1/models"
	}))
	if status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = %s, want authorized", status)
	}
	if len(routes) != 1 || routes[0].Name != "fp-gw" || routes[0].Kind != domain.AgentModelRouteLocal || routes[0].APIFamily != domain.AgentModelRouteFamilyOpenAI ||
		routes[0].Credential != domain.AgentModelRouteCredentialNone || routes[0].Verification != domain.AgentModelRouteVerified || routes[0].ConfigSource == "" {
		t.Fatalf("routes = %+v", routes)
	}
	if !hasRouteCode(steps, "route_verified") {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestCustomProviderNetworkChallengeIsConfigured(t *testing.T) {
	status, routes, _, _ := codexRouteFixture(t, keylessProvider, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://login.example.com/", http.StatusFound)
	})
	if status != ports.AgentAuthStatusConfigured || len(routes) != 1 || routes[0].Verification != domain.AgentModelRouteAccessChallenge {
		t.Fatalf("status = %s routes = %+v", status, routes)
	}
}

const keyedProvider = "model_provider = \"litellm\"\n[model_providers.litellm]\nbase_url = \"{BASE}/v1\"\nenv_key = \"FP_GW_KEY\"\n"

func TestCustomProviderKeyAcceptedAndAnonymousRefused(t *testing.T) {
	t.Setenv("FP_GW_KEY", "sk-test")
	status, routes, _, _ := codexRouteFixture(t, keyedProvider, listing(func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "Bearer sk-test"
	}))
	if status != ports.AgentAuthStatusAuthorized || len(routes) != 1 || routes[0].Credential != domain.AgentModelRouteCredentialAPIKey ||
		routes[0].CredentialSource != "FP_GW_KEY" || routes[0].APIFamily != domain.AgentModelRouteFamilyOpenAICompatible {
		t.Fatalf("status = %s routes = %+v", status, routes)
	}
}

func TestCustomProviderKeyRejected(t *testing.T) {
	t.Setenv("FP_GW_KEY", "sk-revoked")
	status, routes, _, _ := codexRouteFixture(t, keyedProvider, listing(func(*http.Request) bool { return false }))
	if status != ports.AgentAuthStatusUnauthorized || len(routes) != 1 || routes[0].Verification != domain.AgentModelRouteRejected {
		t.Fatalf("status = %s routes = %+v", status, routes)
	}
}

func TestCustomProviderEnvKeyUnsetStaysUnknown(t *testing.T) {
	t.Setenv("FP_GW_KEY", "")
	status, routes, steps, _ := codexRouteFixture(t, keyedProvider, func(http.ResponseWriter, *http.Request) {
		t.Error("checked a route without its key")
	})
	if status != ports.AgentAuthStatusUnknown || len(routes) != 1 || routes[0].Verification != domain.AgentModelRouteNotAttempted || !hasRouteCode(steps, "route_credential_missing") {
		t.Fatalf("status = %s routes = %+v steps = %+v", status, routes, steps)
	}
}

func TestBuiltInLocalProviderUsesItsDefaultPort(t *testing.T) {
	status, routes, _, _ := codexRouteFixture(t, "model_provider = \"ollama\"\n", nil)
	if status != ports.AgentAuthStatusConfigured || len(routes) != 1 || routes[0].Host != "localhost:11434" || routes[0].Kind != domain.AgentModelRouteLocal ||
		routes[0].Verification != domain.AgentModelRouteUnreachable {
		t.Fatalf("status = %s routes = %+v", status, routes)
	}
}

func hasRouteCode(steps []domain.AgentAuthDiagnostic, code string) bool {
	for _, step := range steps {
		if step.Code == code {
			return true
		}
	}
	return false
}
