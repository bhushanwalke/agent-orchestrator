package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type triageCall struct {
	decision domain.TriageDecision
	reason   string
	actor    domain.TriageActor
}

func (f *fakePlanService) Triage(_ context.Context, id domain.PlanID, decision domain.TriageDecision, reason string, actor domain.TriageActor) (domain.Plan, error) {
	f.record("triage", id)
	f.gotTriage = triageCall{decision, reason, actor}
	return f.plan, f.err
}

func TestPlansAPI_CreatePassesMode(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"title":"T","brief":"b"}`, ""},
		{`{"title":"T","brief":"b","mode":"plan"}`, "plan"},
		{`{"title":"T","brief":"b","mode":"auto"}`, "auto"},
	} {
		svc := &fakePlanService{plan: samplePlanDomain()}
		srv := newPlanTestServer(t, svc)
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/projects/mer/plans", tc.body)
		if status != http.StatusCreated || string(svc.gotCreate.Mode) != tc.want {
			t.Fatalf("%s: status %d mode %q body %s, want 201 mode %q", tc.body, status, svc.gotCreate.Mode, body, tc.want)
		}
	}
	svc := &fakePlanService{err: apierr.Invalid("INVALID_PLAN_MODE", "mode must be plan or auto", nil)}
	srv := newPlanTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/projects/mer/plans", `{"title":"T","brief":"b","mode":"direct"}`)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "INVALID_PLAN_MODE") {
		t.Fatalf("mode direct = %d %s", status, body)
	}
}

func TestPlansAPI_TriageDecides(t *testing.T) {
	decided := samplePlanDomain()
	decided.State, decided.IntakeMode, decided.DirectSessionID = domain.PlanStateDirect, domain.PlanIntakeAuto, "mer-4"
	decided.ActivatedAt = nil
	decided.Triage = &domain.PlanTriage{Decision: domain.TriageDecisionDirect, Reason: "small", DecidedBy: domain.TriageActorHuman, DecidedAt: planTestTime}
	svc := &fakePlanService{plan: decided}
	srv := newPlanTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/plans/plan_1/triage", `{"decision":"direct","reason":"small"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if svc.gotPlan != "plan_1" || svc.gotTriage != (triageCall{domain.TriageDecisionDirect, "small", domain.TriageActorHuman}) {
		t.Fatalf("triage call = %s %+v, want a human direct decision", svc.gotPlan, svc.gotTriage)
	}
	var got struct {
		Plan struct {
			State, Phase, IntakeMode, DirectSessionID, KanbanColumn, DisplayStatus string
			Triage                                                                 *struct{ Decision, Reason, DecidedBy, DecidedAt string }
		}
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	p := got.Plan
	if p.State != "direct" || p.Phase != "direct" || p.IntakeMode != "auto" || p.DirectSessionID != "mer-4" || p.DisplayStatus != "One worker" ||
		p.Triage == nil || p.Triage.Decision != "direct" || p.Triage.Reason != "small" || p.Triage.DecidedBy != "human" || p.Triage.DecidedAt == "" {
		t.Fatalf("triage response = %s", body)
	}

	if _, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/plans/plan_1/triage", `{"decision":"plan","decidedBy":"orchestrator"}`); status != http.StatusOK ||
		svc.gotTriage != (triageCall{domain.TriageDecisionPlan, "", domain.TriageActorOrchestrator}) {
		t.Fatalf("orchestrator decision = %d %+v", status, svc.gotTriage)
	}
}

func TestPlansAPI_TriageErrors(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		err        error
		status     int
		code       string
	}{
		{"not in triage", `{"decision":"direct"}`, apierr.Conflict("PLAN_NOT_IN_TRIAGE", "already decided", nil), http.StatusConflict, "PLAN_NOT_IN_TRIAGE"},
		{"bad decision", `{"decision":"maybe"}`, apierr.Invalid("INVALID_TRIAGE_DECISION", "bad", nil), http.StatusBadRequest, "INVALID_TRIAGE_DECISION"},
		{"long reason", `{"decision":"plan","reason":"x"}`, apierr.Invalid("INVALID_TRIAGE_REASON", "long", nil), http.StatusBadRequest, "INVALID_TRIAGE_REASON"},
		{"unknown plan", `{"decision":"plan"}`, apierr.NotFound("PLAN_NOT_FOUND", "Unknown plan"), http.StatusNotFound, "PLAN_NOT_FOUND"},
		{"bad actor", `{"decision":"plan","decidedBy":"robot"}`, nil, http.StatusBadRequest, "INVALID_TRIAGE_ACTOR"},
		{"unknown field", `{"decision":"plan","extra":1}`, nil, http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakePlanService{plan: samplePlanDomain(), err: tc.err}
			srv := newPlanTestServer(t, svc)
			body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/plans/plan_1/triage", tc.body)
			if status != tc.status || !strings.Contains(string(body), tc.code) {
				t.Fatalf("triage = %d %s, want %d %s", status, body, tc.status, tc.code)
			}
		})
	}
}
