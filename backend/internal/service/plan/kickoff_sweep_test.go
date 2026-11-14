package plan_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

// A kickoff the orchestrator refused is delivered by a later scheduler tick,
// even with no background retry (a restart or an expired window), once.
func TestTickDeliversDeferredKickoffs(t *testing.T) {
	f := flowFixture(t, domain.ProjectConfig{})
	ctx := context.Background()
	f.sessions.sendErr = apierr.Conflict("SESSION_STARTUP_PENDING", "starting", nil)
	p, err := f.svc.Create(ctx, plan.CreateInput{ProjectID: "mer", Title: "t", Brief: "b"})
	if err != nil {
		t.Fatal(err)
	}
	tick(t, f)
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,kickoff_deferred" {
		t.Fatalf("events while refused = %s", got)
	}
	f.sessions.mu.Lock()
	f.sessions.sendErr = nil
	f.sessions.mu.Unlock()
	tick(t, f)
	tick(t, f)
	if got := strings.Join(eventKinds(t, f, p.ID), ","); got != "plan_created,kickoff_deferred,kickoff_sent" {
		t.Fatalf("events = %s", got)
	}
	if n := len(sentTo(f, "orch-1")); n != 1 {
		t.Fatalf("kickoff delivered %d times", n)
	}
}
