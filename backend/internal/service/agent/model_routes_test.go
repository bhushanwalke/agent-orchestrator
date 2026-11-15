package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type routeReadinessAgent struct {
	accountReadinessAgent
	routes []domain.AgentModelRoute
}

func (a *routeReadinessAgent) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	for _, route := range a.routes {
		ports.RecordModelRoute(ctx, route)
	}
	return a.status, a.account, nil, nil
}

func TestReadinessCoordinatorAttachesSanitizedRoutesAndNamesThem(t *testing.T) {
	a := &routeReadinessAgent{
		accountReadinessAgent: accountReadinessAgent{status: ports.AgentAuthStatusConfigured},
		routes: []domain.AgentModelRoute{{
			Kind: domain.AgentModelRouteCloud, APIFamily: domain.AgentModelRouteFamilyBedrock, Host: "gw.example.com",
			Credential: domain.AgentModelRouteCredentialNone, Verification: domain.AgentModelRouteUnverifiable,
		}, {Kind: "bogus"}},
	}
	c := newReadinessCoordinator(readinessCoordinatorConfig{})
	observation, failed := c.checkAuthentication(readinessHarness("claude-code", "Claude Code", a), domain.AgentReadinessPurposeDisplay)
	if failed || observation.State != domain.AgentAuthenticationConfigured {
		t.Fatalf("observation = %+v failed = %v", observation, failed)
	}
	if len(observation.Routes) != 1 || observation.Routes[0].Host != "gw.example.com" {
		t.Fatalf("routes = %+v", observation.Routes)
	}
	want := "Claude Code is configured to use AWS Bedrock via gw.example.com with no key. Orchestrator could not verify it without starting a session."
	if observation.Reason != want {
		t.Fatalf("reason = %q\nwant     %q", observation.Reason, want)
	}
	if got := domain.EffectiveAgentReadiness(domain.AgentInstallationInstalled, observation.State); got != domain.AgentReadinessUnknown {
		t.Fatalf("readiness = %s, want unknown", got)
	}
}

func TestReadinessCoordinatorAttachesRoutesToEveryVerdict(t *testing.T) {
	route := domain.AgentModelRoute{Name: "fp-gw", Kind: domain.AgentModelRouteGateway, APIFamily: domain.AgentModelRouteFamilyOpenAI, Host: "gw.example.com", Credential: domain.AgentModelRouteCredentialAPIKey}
	for _, status := range []ports.AgentAuthStatus{ports.AgentAuthStatusAuthorized, ports.AgentAuthStatusUnauthorized, ports.AgentAuthStatusUnknown} {
		a := &routeReadinessAgent{accountReadinessAgent: accountReadinessAgent{status: status}, routes: []domain.AgentModelRoute{route}}
		observation, _ := newReadinessCoordinator(readinessCoordinatorConfig{}).checkAuthentication(readinessHarness("codex", "Codex", a), domain.AgentReadinessPurposeDisplay)
		if len(observation.Routes) != 1 || observation.Routes[0].Name != "fp-gw" {
			t.Fatalf("%s: routes = %+v", status, observation.Routes)
		}
		if strings.Contains(observation.Reason, "configured to use") {
			t.Fatalf("%s: reason rewritten: %q", status, observation.Reason)
		}
	}
}

func TestDescribeRoutes(t *testing.T) {
	gateway := func(name, host string) domain.AgentModelRoute {
		return domain.AgentModelRoute{Name: name, Kind: domain.AgentModelRouteGateway, APIFamily: domain.AgentModelRouteFamilyOpenAI, Host: host, Credential: domain.AgentModelRouteCredentialAPIKey}
	}
	cases := []struct {
		routes []domain.AgentModelRoute
		want   string
	}{
		{[]domain.AgentModelRoute{gateway("fp-gw", "gw.example.com")}, "provider fp-gw (OpenAI API via gw.example.com)"},
		{[]domain.AgentModelRoute{gateway("", "gw.example.com")}, "OpenAI API via gw.example.com"},
		{[]domain.AgentModelRoute{gateway("a", "gw.example.com"), gateway("b", "gw.example.com")}, "several providers via gw.example.com"},
		{[]domain.AgentModelRoute{gateway("a", "gw.example.com"), gateway("b", "other.example.com")}, "several providers"},
		{[]domain.AgentModelRoute{{Kind: domain.AgentModelRouteCloud, APIFamily: domain.AgentModelRouteFamilyVertex}}, "Google Vertex AI"},
	}
	for _, c := range cases {
		if got := describeRoutes(c.routes); got != c.want {
			t.Errorf("describeRoutes = %q, want %q", got, c.want)
		}
	}
}
