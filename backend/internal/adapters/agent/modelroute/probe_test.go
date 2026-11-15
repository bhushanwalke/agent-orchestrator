package modelroute

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestListURL(t *testing.T) {
	cases := []struct {
		base   string
		family domain.AgentModelRouteFamily
		want   string
	}{
		{"https://gw.example.com/v1", domain.AgentModelRouteFamilyOpenAI, "https://gw.example.com/v1/models"},
		{"https://gw.example.com/v1/", domain.AgentModelRouteFamilyOpenAICompatible, "https://gw.example.com/v1/models"},
		{"https://gw.example.com", domain.AgentModelRouteFamilyAnthropic, "https://gw.example.com/v1/models"},
		{"https://gw.example.com/anthropic/v1", domain.AgentModelRouteFamilyAnthropic, "https://gw.example.com/anthropic/v1/models"},
		{"https://gw.example.com/openai?api-version=1", domain.AgentModelRouteFamilyOpenAI, "https://gw.example.com/openai/models?api-version=1"},
		{"https://gw.example.com", domain.AgentModelRouteFamilyBedrock, ""},
		{"https://gw.example.com", domain.AgentModelRouteFamilyVertex, ""},
		{"https://gw.example.com", domain.AgentModelRouteFamilyGoogle, ""},
		{"https://gw.example.com", domain.AgentModelRouteFamilyOther, ""},
		{"ftp://gw.example.com", domain.AgentModelRouteFamilyOpenAI, ""},
		{"gw.example.com/v1", domain.AgentModelRouteFamilyOpenAI, ""},
	}
	for _, c := range cases {
		if got := ListURL(c.base, c.family); got != c.want {
			t.Errorf("ListURL(%q, %s) = %q, want %q", c.base, c.family, got, c.want)
		}
	}
}

func resetForTest(t *testing.T) {
	t.Helper()
	InvalidateCache()
	t.Setenv("AO_MODEL_ROUTE_PROBE", "")
	t.Cleanup(InvalidateCache)
}

func serve(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

func reply(status int, contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestCheckClassifiesResponses(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    domain.AgentModelRouteVerification
	}{
		{"openai list", reply(200, "application/json", `{"object":"list","data":[{"id":"m"}]}`), domain.AgentModelRouteVerified},
		{"gemini-style list", reply(200, "application/json", `{"models":[]}`), domain.AgentModelRouteVerified},
		{"json without a list", reply(200, "application/json", `{"ok":true}`), domain.AgentModelRouteInconclusive},
		{"json 401", reply(401, "application/json", `{"error":"bad key"}`), domain.AgentModelRouteRejected},
		{"html 403", reply(403, "text/html", `<html>sign in</html>`), domain.AgentModelRouteAccessChallenge},
		{"html 200", reply(200, "text/html", `<!doctype html><title>Login</title>`), domain.AgentModelRouteAccessChallenge},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/login", http.StatusFound) }, domain.AgentModelRouteAccessChallenge},
		{"404", reply(404, "application/json", `{"error":"no"}`), domain.AgentModelRouteInconclusive},
		{"500", reply(500, "", ``), domain.AgentModelRouteInconclusive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetForTest(t)
			server, hits := serve(t, c.handler)
			result := Check(context.Background(), Request{BaseURL: server.URL + "/v1", Family: domain.AgentModelRouteFamilyOpenAICompatible})
			if result.Verification != c.want {
				t.Fatalf("verification = %s, want %s (status %d)", result.Verification, c.want, result.HTTPStatus)
			}
			if c.name == "redirect" && hits.Load() != 1 {
				t.Fatalf("redirect followed: %d requests", hits.Load())
			}
		})
	}
}

func TestCheckSendsTheHarnessCredentialAndRejectsAPublicListing(t *testing.T) {
	resetForTest(t)
	keyed, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			reply(401, "application/json", `{"error":"no key"}`)(w, r)
			return
		}
		reply(200, "application/json", `{"data":[]}`)(w, r)
	})
	headers := http.Header{"Authorization": {"Bearer sk-test"}}
	if got := Check(context.Background(), Request{BaseURL: keyed.URL, Family: domain.AgentModelRouteFamilyOpenAI, Headers: headers}); got.Verification != domain.AgentModelRouteVerified {
		t.Fatalf("keyed listing = %s", got.Verification)
	}

	public, hits := serve(t, reply(200, "application/json", `{"data":[]}`))
	if got := Check(context.Background(), Request{BaseURL: public.URL, Family: domain.AgentModelRouteFamilyOpenAI, Headers: headers}); got.Verification != domain.AgentModelRouteInconclusive {
		t.Fatalf("public listing with a key = %s, want inconclusive", got.Verification)
	}
	if hits.Load() != 2 {
		t.Fatalf("public listing requests = %d, want 2 (with and without the key)", hits.Load())
	}
}

func TestCheckSendsAnthropicHeaders(t *testing.T) {
	resetForTest(t)
	var version, path string
	server, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		version, path = r.Header.Get("anthropic-version"), r.URL.Path
		reply(200, "application/json", `{"data":[]}`)(w, r)
	})
	if got := Check(context.Background(), Request{BaseURL: server.URL, Family: domain.AgentModelRouteFamilyAnthropic}); got.Verification != domain.AgentModelRouteVerified {
		t.Fatalf("verification = %s", got.Verification)
	}
	if version == "" || path != "/v1/models" {
		t.Fatalf("anthropic-version %q, path %q", version, path)
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Error("request sent")
	return nil, errors.New("no network in tests")
}

func TestCheckNeverSendsAKeyInCleartextOffLoopback(t *testing.T) {
	resetForTest(t)
	previous := Client
	Client = &http.Client{Transport: failingTransport{t}}
	t.Cleanup(func() { Client = previous })
	got := Check(context.Background(), Request{BaseURL: "http://gw.example.invalid/v1", Family: domain.AgentModelRouteFamilyOpenAI, Headers: http.Header{"Authorization": {"Bearer sk"}}})
	if got.Verification != domain.AgentModelRouteInconclusive {
		t.Fatalf("verification = %s", got.Verification)
	}
	got = Check(context.Background(), Request{BaseURL: "https://user:pw@gw.example.invalid/v1", Family: domain.AgentModelRouteFamilyOpenAI})
	if got.Verification != domain.AgentModelRouteInconclusive {
		t.Fatalf("userinfo verification = %s", got.Verification)
	}
}

func TestCheckCapsTheBodyAndTimesOut(t *testing.T) {
	resetForTest(t)
	huge, _ := serve(t, reply(200, "application/json", `{"data":["`+strings.Repeat("x", 2<<20)+`"]}`))
	if got := Check(context.Background(), Request{BaseURL: huge.URL, Family: domain.AgentModelRouteFamilyOpenAI}); got.Verification != domain.AgentModelRouteInconclusive {
		t.Fatalf("oversized body = %s, want inconclusive", got.Verification)
	}

	previous := probeTimeout
	probeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { probeTimeout = previous })
	slow, _ := serve(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	if got := Check(context.Background(), Request{BaseURL: slow.URL, Family: domain.AgentModelRouteFamilyOpenAI}); got.Verification != domain.AgentModelRouteUnreachable {
		t.Fatalf("slow endpoint = %s, want unreachable", got.Verification)
	}
}

func TestCheckCachesAndInvalidates(t *testing.T) {
	resetForTest(t)
	server, hits := serve(t, reply(200, "application/json", `{"data":[]}`))
	req := Request{BaseURL: server.URL, Family: domain.AgentModelRouteFamilyOpenAI}
	first := Check(context.Background(), req)
	second := Check(context.Background(), req)
	if first.Cached || !second.Cached || second.Verification != domain.AgentModelRouteVerified || hits.Load() != 1 {
		t.Fatalf("first %+v second %+v hits %d", first, second, hits.Load())
	}
	// A different credential is a different cache entry.
	if got := Check(context.Background(), Request{BaseURL: server.URL, Family: domain.AgentModelRouteFamilyOpenAI, Headers: http.Header{"Authorization": {"Bearer k"}}}); got.Cached {
		t.Fatal("cache ignored the credential")
	}
	InvalidateCache()
	hits.Store(0)
	if got := Check(context.Background(), req); got.Cached || hits.Load() != 1 {
		t.Fatalf("after invalidation cached=%v hits=%d", got.Cached, hits.Load())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	InvalidateCache()
	_ = Check(cancelled, req)
	if got := Check(context.Background(), req); got.Cached {
		t.Fatal("a cancelled check was cached")
	}
}

func TestCheckHonoursTheOffSwitchAtCheckTime(t *testing.T) {
	resetForTest(t)
	server, hits := serve(t, reply(200, "application/json", `{"data":[]}`))
	t.Setenv("AO_MODEL_ROUTE_PROBE", "off")
	if got := Check(context.Background(), Request{BaseURL: server.URL, Family: domain.AgentModelRouteFamilyOpenAI}); got.Verification != domain.AgentModelRouteDisabled || hits.Load() != 0 {
		t.Fatalf("verification = %s hits %d", got.Verification, hits.Load())
	}
	if got := Check(context.Background(), Request{BaseURL: "https://gw.example.com", Family: domain.AgentModelRouteFamilyBedrock}); got.Verification != domain.AgentModelRouteDisabled {
		t.Fatalf("bedrock with probes off = %s", got.Verification)
	}
	t.Setenv("AO_MODEL_ROUTE_PROBE", "")
	if got := Check(context.Background(), Request{BaseURL: "https://gw.example.com", Family: domain.AgentModelRouteFamilyBedrock}); got.Verification != domain.AgentModelRouteUnverifiable {
		t.Fatalf("bedrock = %s", got.Verification)
	}
}

func TestRecordAddsTheRouteAndASafeDiagnostic(t *testing.T) {
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	route := Record(ctx, domain.AgentModelRoute{Name: "fp-gw", Kind: domain.AgentModelRouteGateway, Host: "gw.example.com"}, Result{Verification: domain.AgentModelRouteRejected, HTTPStatus: 401})
	if route.Verification != domain.AgentModelRouteRejected || route.HTTPStatus != 401 {
		t.Fatalf("route = %+v", route)
	}
	if got := evidence.Routes(); len(got) != 1 {
		t.Fatalf("routes = %+v", got)
	}
	diagnostics := evidence.Diagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Code != "route_rejected" || diagnostics[0].Source != "fp-gw · gw.example.com" || diagnostics[0].Method != "model_route" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	RecordMissingCredential(ctx, domain.AgentModelRoute{Name: "x", Kind: domain.AgentModelRouteGateway, CredentialSource: "GW_KEY"})
	diagnostics = evidence.Diagnostics()
	if last := diagnostics[len(diagnostics)-1]; last.Code != "route_credential_missing" || last.Source != "x" {
		t.Fatalf("missing credential diagnostic = %+v", last)
	}
}

func TestKind(t *testing.T) {
	if Kind("localhost:11434") != domain.AgentModelRouteLocal || Kind("gw.example.com") != domain.AgentModelRouteGateway {
		t.Fatal("kind")
	}
}

func TestStatusAggregatesRoutes(t *testing.T) {
	route := func(v domain.AgentModelRouteVerification) domain.AgentModelRoute {
		return domain.AgentModelRoute{Kind: domain.AgentModelRouteGateway, Verification: v}
	}
	cases := []struct {
		routes []domain.AgentModelRoute
		want   ports.AgentAuthStatus
		ok     bool
	}{
		{nil, ports.AgentAuthStatusUnknown, false},
		{[]domain.AgentModelRoute{route(domain.AgentModelRouteRejected), route(domain.AgentModelRouteVerified)}, ports.AgentAuthStatusAuthorized, true},
		{[]domain.AgentModelRoute{route(domain.AgentModelRouteRejected), route(domain.AgentModelRouteRejected)}, ports.AgentAuthStatusUnauthorized, true},
		{[]domain.AgentModelRoute{route(domain.AgentModelRouteRejected), route(domain.AgentModelRouteUnverifiable)}, ports.AgentAuthStatusConfigured, true},
		{[]domain.AgentModelRoute{route(domain.AgentModelRouteAccessChallenge)}, ports.AgentAuthStatusConfigured, true},
	}
	for i, c := range cases {
		got, ok := Status(c.routes)
		if got != c.want || ok != c.ok {
			t.Errorf("case %d: Status = %s, %v; want %s, %v", i, got, ok, c.want, c.ok)
		}
	}
}

func TestDecideKeepsADefiniteVerdictAndRecordsTheVerifiedRoute(t *testing.T) {
	verified := domain.AgentModelRoute{Name: "gw", Kind: domain.AgentModelRouteGateway, Credential: domain.AgentModelRouteCredentialAPIKey, CredentialSource: "GW_KEY", Verification: domain.AgentModelRouteVerified}
	rejected := domain.AgentModelRoute{Kind: domain.AgentModelRouteGateway, Verification: domain.AgentModelRouteRejected}
	if got := Decide(context.Background(), ports.AgentAuthStatusAuthorized, []domain.AgentModelRoute{rejected}); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("authorized replaced by %s", got)
	}
	if got := Decide(context.Background(), ports.AgentAuthStatusUnauthorized, []domain.AgentModelRoute{verified}); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("unauthorized replaced by %s", got)
	}
	if got := Decide(context.Background(), ports.AgentAuthStatusConfigured, nil); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("no routes = %s", got)
	}
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	if got := Decide(ctx, ports.AgentAuthStatusUnknown, []domain.AgentModelRoute{rejected, verified}); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("verified route = %s", got)
	}
	identity := evidence.Identity()
	if identity.Method != domain.AgentAccountMethodAPIKey || identity.Source != "GW_KEY" || identity.Provider != "gw" {
		t.Fatalf("identity = %+v", identity)
	}
}
