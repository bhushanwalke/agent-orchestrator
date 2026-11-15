package ports_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRecordModelRouteReplacesTheSameRouteAndIsBounded(t *testing.T) {
	ports.RecordModelRoute(context.Background(), domain.AgentModelRoute{Kind: domain.AgentModelRouteGateway}) // no collector: no-op
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	route := domain.AgentModelRoute{Name: "gw", Kind: domain.AgentModelRouteGateway, APIFamily: domain.AgentModelRouteFamilyOpenAI, Host: "gw.example.com"}
	ports.RecordModelRoute(ctx, route)
	route.Verification = domain.AgentModelRouteVerified
	ports.RecordModelRoute(ctx, route)
	if got := evidence.Routes(); len(got) != 1 || got[0].Verification != domain.AgentModelRouteVerified {
		t.Fatalf("routes = %+v", got)
	}
	for i := range 30 {
		ports.RecordModelRoute(ctx, domain.AgentModelRoute{Name: fmt.Sprint(i), Kind: domain.AgentModelRouteGateway})
	}
	if got := evidence.Routes(); len(got) != domain.MaxAgentModelRoutes {
		t.Fatalf("len = %d", len(got))
	}
	if (*ports.AuthEvidence)(nil).Routes() != nil {
		t.Fatal("nil evidence has routes")
	}
}
