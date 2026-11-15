package claudecode

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelroute"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func stubClaudeCLI(t *testing.T, stdout string) {
	t.Helper()
	previous := claudeAuthCommand
	claudeAuthCommand = func(context.Context, string, string, map[string]string) (claudeCommandOutput, error) {
		if stdout == "" {
			return claudeCommandOutput{}, errors.New("claude not runnable")
		}
		return claudeCommandOutput{Stdout: []byte(stdout)}, nil
	}
	t.Cleanup(func() { claudeAuthCommand = previous })
}

func resetRouteCaches(t *testing.T) {
	t.Helper()
	InvalidateAuthCache()
	modelroute.InvalidateCache()
	t.Cleanup(func() {
		InvalidateAuthCache()
		modelroute.InvalidateCache()
	})
}

type refusingTransport struct{ t *testing.T }

func (r refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.t.Errorf("unexpected request to %s", req.URL.Host)
	return nil, errors.New("no network in tests")
}

func refuseRouteRequests(t *testing.T) {
	t.Helper()
	previous := modelroute.Client
	modelroute.Client = &http.Client{Transport: refusingTransport{t}}
	t.Cleanup(func() { modelroute.Client = previous })
}

func checkClaude(t *testing.T) (ports.AgentAuthStatus, []domain.AgentModelRoute, []domain.AgentAuthDiagnostic) {
	t.Helper()
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, _, steps, err := (&Plugin{resolvedBinary: "/fixture/claude"}).AuthStatusWithDiagnostics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return status, evidence.Routes(), append(steps, evidence.Diagnostics()...)
}

func hasCode(steps []domain.AgentAuthDiagnostic, code string) bool {
	for _, step := range steps {
		if step.Code == code {
			return true
		}
	}
	return false
}

func TestBedrockFromUserSettingsIsConfiguredWithoutARequest(t *testing.T) {
	clearClaudeCredentialEnv(t)
	resetRouteCaches(t)
	refuseRouteRequests(t)
	stubClaudeCLI(t, `{"loggedIn":true,"apiProvider":"bedrock"}`)
	path := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","CLAUDE_CODE_SKIP_BEDROCK_AUTH":true,"ANTHROPIC_BEDROCK_BASE_URL":"https://user:pw@gw.example.com/bedrock?x=1"}}`
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	status, routes, steps := checkClaude(t)
	if status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %s, want configured", status)
	}
	want := domain.AgentModelRoute{
		Kind: domain.AgentModelRouteCloud, APIFamily: domain.AgentModelRouteFamilyBedrock, Host: "gw.example.com",
		Credential: domain.AgentModelRouteCredentialNone, ConfigSource: "~/.claude/settings.json", Verification: domain.AgentModelRouteUnverifiable,
	}
	if len(routes) != 1 || routes[0] != want {
		t.Fatalf("routes = %+v\nwant %+v", routes, want)
	}
	if !hasCode(steps, "route_unverifiable") {
		t.Fatalf("no route_unverifiable diagnostic: %+v", steps)
	}
}

func TestBedrockWithAnAWSProfileAndNoCLIIsConfigured(t *testing.T) {
	clearClaudeCredentialEnv(t)
	resetRouteCaches(t)
	refuseRouteRequests(t)
	stubClaudeCLI(t, "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("AWS_PROFILE", "work")
	status, routes, _ := checkClaude(t)
	if status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %s, want configured", status)
	}
	if len(routes) != 1 || routes[0].Host != "bedrock-runtime.eu-west-1.amazonaws.com" || routes[0].Credential != domain.AgentModelRouteCredentialCloudCredentials ||
		routes[0].CredentialSource != "AWS_PROFILE" || routes[0].ConfigSource != "CLAUDE_CODE_USE_BEDROCK" {
		t.Fatalf("routes = %+v", routes)
	}
}

func TestVertexAndFoundryRoutes(t *testing.T) {
	for _, tc := range []struct {
		env        map[string]string
		family     domain.AgentModelRouteFamily
		host       string
		credential domain.AgentModelRouteCredential
	}{
		{map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLOUD_ML_REGION": "us-east5"}, domain.AgentModelRouteFamilyVertex, "us-east5-aiplatform.googleapis.com", domain.AgentModelRouteCredentialCloudCredentials},
		{map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLOUD_ML_REGION": "global", "CLAUDE_CODE_SKIP_VERTEX_AUTH": "1"}, domain.AgentModelRouteFamilyVertex, "aiplatform.googleapis.com", domain.AgentModelRouteCredentialNone},
		{map[string]string{"CLAUDE_CODE_USE_FOUNDRY": "1", "ANTHROPIC_FOUNDRY_RESOURCE": "acme", "ANTHROPIC_FOUNDRY_API_KEY": "k"}, domain.AgentModelRouteFamilyFoundry, "acme.services.ai.azure.com", domain.AgentModelRouteCredentialAPIKey},
	} {
		t.Run(string(tc.family)+tc.host, func(t *testing.T) {
			clearClaudeCredentialEnv(t)
			resetRouteCaches(t)
			refuseRouteRequests(t)
			stubClaudeCLI(t, "")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			status, routes, _ := checkClaude(t)
			if status != ports.AgentAuthStatusConfigured || len(routes) != 1 || routes[0].APIFamily != tc.family || routes[0].Host != tc.host || routes[0].Credential != tc.credential {
				t.Fatalf("status = %s routes = %+v", status, routes)
			}
		})
	}
}

func TestKeylessGatewayIsDecidedByItsModelList(t *testing.T) {
	for _, tc := range []struct {
		name         string
		handler      http.HandlerFunc
		status       ports.AgentAuthStatus
		verification domain.AgentModelRouteVerification
	}{
		{"listing", func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != "" || r.Header.Get("Authorization") != "" {
				t.Error("a keyless route sent a credential")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"claude"}]}`))
		}, ports.AgentAuthStatusAuthorized, domain.AgentModelRouteVerified},
		{"zero-trust page", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<html>Sign in to continue</html>`))
		}, ports.AgentAuthStatusConfigured, domain.AgentModelRouteAccessChallenge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearClaudeCredentialEnv(t)
			resetRouteCaches(t)
			stubClaudeCLI(t, `{"loggedIn":false,"apiProvider":"firstParty"}`)
			server := httptest.NewServer(tc.handler)
			t.Cleanup(server.Close)
			t.Setenv("ANTHROPIC_BASE_URL", server.URL)
			status, routes, steps := checkClaude(t)
			if status != tc.status {
				t.Fatalf("status = %s, want %s", status, tc.status)
			}
			if len(routes) != 1 || routes[0].Kind != domain.AgentModelRouteLocal || routes[0].APIFamily != domain.AgentModelRouteFamilyAnthropic ||
				routes[0].Credential != domain.AgentModelRouteCredentialNone || routes[0].Verification != tc.verification || routes[0].ConfigSource != "ANTHROPIC_BASE_URL" {
				t.Fatalf("routes = %+v", routes)
			}
			if !hasCode(steps, tc.verification.DiagnosticCode()) {
				t.Fatalf("diagnostics = %+v", steps)
			}
		})
	}
}

func TestKeylessGatewayWithAnAPIKeyHelperIsNotProbed(t *testing.T) {
	clearClaudeCredentialEnv(t)
	resetRouteCaches(t)
	refuseRouteRequests(t)
	stubClaudeCLI(t, "")
	path := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"apiKeyHelper":"~/bin/key","env":{"ANTHROPIC_BASE_URL":"https://gw.example.com"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	status, routes, _ := checkClaude(t)
	if status != ports.AgentAuthStatusConfigured || len(routes) != 1 || routes[0].Credential != domain.AgentModelRouteCredentialUnknown ||
		routes[0].Verification != domain.AgentModelRouteNotAttempted {
		t.Fatalf("status = %s routes = %+v", status, routes)
	}
}

func TestFirstPartyRecordsNoRoute(t *testing.T) {
	clearClaudeCredentialEnv(t)
	resetRouteCaches(t)
	refuseRouteRequests(t)
	stubClaudeCLI(t, `{"loggedIn":false,"apiProvider":"firstParty"}`)
	status, routes, _ := checkClaude(t)
	if status != ports.AgentAuthStatusUnauthorized || len(routes) != 0 {
		t.Fatalf("status = %s routes = %+v", status, routes)
	}
}

func TestARouteRejectionNeverBlocksALaunch(t *testing.T) {
	clearClaudeCredentialEnv(t)
	resetRouteCaches(t)
	stubClaudeCLI(t, "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"no key"}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)
	if status, _, _ := checkClaude(t); status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("readiness status = %s, want unauthorized", status)
	}
	status, err := (&Plugin{resolvedBinary: "/fixture/claude"}).ValidateLaunchAuth(context.Background(), "", nil)
	if err != nil || status == ports.AgentAuthStatusUnauthorized {
		t.Fatalf("launch status = %s err = %v; a route rejection blocked the launch", status, err)
	}
}
