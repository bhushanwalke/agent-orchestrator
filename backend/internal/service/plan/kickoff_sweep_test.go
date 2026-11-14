package plan_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// A brand-new orchestrator refuses the first kickoff while its controller
// starts; that is waiting, not a failure.
func TestFreshOrchestratorKickoffIsWaitingNotDeferred(t *testing.T) {
	cases := []struct {
		name         string
		createdAt    time.Time
		provisioning bool
		want, absent string
	}{
		{"created by this Create", time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC), false, "kickoff_waiting", "kickoff_deferred"},
		{"still provisioning", time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC), true, "kickoff_waiting", "kickoff_deferred"},
		{"long-running orchestrator refuses", time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC), false, "kickoff_deferred", "kickoff_waiting"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := flowFixture(t, domain.ProjectConfig{})
			f.sessions.orchCreatedAt = tc.createdAt
			f.sessions.orchProvisioning = tc.provisioning
			f.sessions.sendErrFor = map[domain.SessionID]error{"orch-1": errors.New("controller not running")}
			p, err := f.svc.Create(context.Background(), plan.CreateInput{ProjectID: "mer", Title: "Todo", Brief: "Build it"})
			if err != nil {
				t.Fatal(err)
			}
			kinds := strings.Join(eventKinds(t, f, p.ID), ",")
			if !strings.Contains(kinds, tc.want) || strings.Contains(kinds, tc.absent) {
				t.Fatalf("events = %s", kinds)
			}
		})
	}
}
