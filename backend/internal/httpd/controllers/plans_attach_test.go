package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	plansvc "github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

func (f *fakePlanService) Lookup(_ context.Context, id domain.PlanID) (domain.Plan, error) {
	f.record("lookup", id)
	if f.err != nil {
		return domain.Plan{}, f.err
	}
	if id != f.plan.ID {
		return domain.Plan{}, apierr.NotFound("PLAN_NOT_FOUND", "Unknown plan")
	}
	return f.plan, nil
}

func newAttachTestServer(t *testing.T, sessions *fakeSessionService, plans *fakePlanService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: sessions, Plans: plans}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionsAPI_SpawnAttachesToPlanWithoutMilestone(t *testing.T) {
	sessions := newFakeSessionService()
	plans := &fakePlanService{plan: domain.Plan{ID: "plan_1", ProjectID: "ao"}}
	srv := newAttachTestServer(t, sessions, plans)
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"ao","kind":"worker","harness":"codex","prompt":"go","planId":" plan_1 "}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, body)
	}
	pc := sessions.lastSpawn.PlanContext
	if pc == nil || pc.PlanID != "plan_1" || pc.MilestoneID != "" || pc.MilestoneKey != "" || pc.PromptSection != "" {
		t.Fatalf("plan context = %+v", pc)
	}
}

func TestSessionsAPI_SpawnRejectsUnknownOrForeignPlan(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"unknown", `{"projectId":"ao","prompt":"go","planId":"plan_9"}`, "PLAN_NOT_FOUND", http.StatusNotFound},
		{"other project", `{"projectId":"web","prompt":"go","planId":"plan_1"}`, "PLAN_PROJECT_MISMATCH", http.StatusBadRequest},
		{"no project", `{"prompt":"go","planId":"plan_1"}`, "PLAN_PROJECT_MISMATCH", http.StatusBadRequest},
		{"orchestrator", `{"projectId":"ao","kind":"orchestrator","prompt":"go","planId":"plan_1"}`, "PLAN_ATTACH_WORKER_ONLY", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := newFakeSessionService()
			plans := &fakePlanService{plan: domain.Plan{ID: "plan_1", ProjectID: "ao"}}
			srv := newAttachTestServer(t, sessions, plans)
			body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", tc.body)
			if status != tc.status || !strings.Contains(string(body), tc.code) {
				t.Fatalf("spawn = %d %s, want %d %s", status, body, tc.status, tc.code)
			}
			if sessions.lastSpawn.ProjectID != "" || sessions.lastSpawn.PlanContext != nil {
				t.Fatalf("spawned despite the error: %+v", sessions.lastSpawn)
			}
		})
	}
}

func TestPlansAPI_DetailListsResearchSessions(t *testing.T) {
	sess := domain.Session{SessionRecord: domain.SessionRecord{ID: "ao-7", DisplayName: "Research: storage", PlanID: "plan_1",
		Metadata: domain.SessionMetadata{Branch: "ao/ao-7/root"}}, Status: domain.StatusWorking}
	svc := &fakePlanService{detail: plansvc.Detail{Summary: plansvc.Summary{Plan: samplePlanDomain()}, ResearchSessions: []domain.Session{sess}}}
	srv := newPlanTestServer(t, svc)
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/plans/plan_1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d body=%s", status, body)
	}
	var resp struct {
		ResearchSessions []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Status      string `json:"status"`
			Branch      string `json:"branch"`
		} `json:"researchSessions"`
	}
	mustJSON(t, body, &resp)
	if len(resp.ResearchSessions) != 1 || resp.ResearchSessions[0].ID != "ao-7" || resp.ResearchSessions[0].DisplayName != "Research: storage" ||
		resp.ResearchSessions[0].Status != string(domain.StatusWorking) || resp.ResearchSessions[0].Branch != "ao/ao-7/root" {
		t.Fatalf("research sessions = %+v body=%s", resp.ResearchSessions, body)
	}
	empty := &fakePlanService{detail: plansvc.Detail{Summary: plansvc.Summary{Plan: samplePlanDomain()}}}
	body, _, _ = doRequest(t, newPlanTestServer(t, empty), http.MethodGet, "/api/v1/plans/plan_1", "")
	if !strings.Contains(string(body), `"researchSessions":[]`) {
		t.Fatalf("empty research sessions not serialised as []: %s", body)
	}
}
