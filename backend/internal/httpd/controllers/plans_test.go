package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	plansvc "github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type fakePlanService struct {
	calls      []string
	gotProject domain.ProjectID
	gotPlan    domain.PlanID
	gotKey     string
	gotCreate  plansvc.CreateInput
	gotPatch   plansvc.Patch
	gotMsIn    plansvc.MilestoneInput
	gotMsPatch plansvc.MilestonePatch
	gotQA      plansvc.QAResultInput
	gotTriage  triageCall
	gotAfter   int64
	gotLimit   int
	plan       domain.Plan
	summary    plansvc.Summary
	detail     plansvc.Detail
	summaries  []plansvc.Summary
	milestone  domain.Milestone
	events     plansvc.EventsPage
	err        error
}

func (f *fakePlanService) record(name string, id domain.PlanID) {
	f.calls = append(f.calls, name)
	f.gotPlan = id
}

func (f *fakePlanService) Create(_ context.Context, in plansvc.CreateInput) (domain.Plan, error) {
	f.calls = append(f.calls, "create")
	f.gotCreate = in
	return f.plan, f.err
}
func (f *fakePlanService) Get(_ context.Context, id domain.PlanID) (plansvc.Detail, error) {
	f.record("get", id)
	return f.detail, f.err
}
func (f *fakePlanService) List(_ context.Context, projectID domain.ProjectID) ([]plansvc.Summary, error) {
	f.calls = append(f.calls, "list")
	f.gotProject = projectID
	return f.summaries, f.err
}
func (f *fakePlanService) Update(_ context.Context, id domain.PlanID, p plansvc.Patch) (plansvc.Summary, error) {
	f.record("update", id)
	f.gotPatch = p
	return f.summary, f.err
}
func (f *fakePlanService) AddMilestone(_ context.Context, id domain.PlanID, in plansvc.MilestoneInput) (domain.Milestone, error) {
	f.record("addMilestone", id)
	f.gotMsIn = in
	return f.milestone, f.err
}
func (f *fakePlanService) UpdateMilestone(_ context.Context, id domain.PlanID, key string, p plansvc.MilestonePatch) (domain.Milestone, error) {
	f.record("updateMilestone", id)
	f.gotKey = key
	f.gotMsPatch = p
	return f.milestone, f.err
}
func (f *fakePlanService) RemoveMilestone(_ context.Context, id domain.PlanID, key string) error {
	f.record("removeMilestone", id)
	f.gotKey = key
	return f.err
}
func (f *fakePlanService) Activate(_ context.Context, id domain.PlanID) (plansvc.Detail, error) {
	f.record("activate", id)
	return f.detail, f.err
}
func (f *fakePlanService) Cancel(_ context.Context, id domain.PlanID) (plansvc.Summary, error) {
	f.record("cancel", id)
	return f.summary, f.err
}
func (f *fakePlanService) RetryMilestone(_ context.Context, id domain.PlanID, key string) (plansvc.Detail, error) {
	f.record("retry", id)
	f.gotKey = key
	return f.detail, f.err
}
func (f *fakePlanService) RecordQAResult(_ context.Context, id domain.PlanID, in plansvc.QAResultInput) (plansvc.Detail, error) {
	f.record("qaResult", id)
	f.gotQA = in
	return f.detail, f.err
}
func (f *fakePlanService) Events(_ context.Context, id domain.PlanID, after int64, limit int) (plansvc.EventsPage, error) {
	f.record("events", id)
	f.gotAfter, f.gotLimit = after, limit
	return f.events, f.err
}

func newPlanTestServer(t *testing.T, svc controllers.PlanService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Plans: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

var planTestTime = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

func samplePlanDomain() domain.Plan {
	return domain.Plan{
		ID: "plan_1", ProjectID: "mer", Title: "Demo", Brief: "Add X", TicketRef: "github:o/r#1",
		State: domain.PlanStateActive, OrchestratorSessionID: "orch-1", MaxParallel: 3,
		CreatedAt: planTestTime, UpdatedAt: planTestTime, ActivatedAt: &planTestTime,
	}
}

func sampleMilestoneDomain() domain.Milestone {
	return domain.Milestone{
		ID: "ms_1", PlanID: "plan_1", Key: "m1", Kind: domain.MilestoneKindImplement, Title: "API",
		Description: "build it", Status: domain.MilestoneStatusRunning, SessionID: "w-1",
		SparsePaths: []string{"src/api"}, DependsOn: []string{}, Attempt: 1,
		CreatedAt: planTestTime, UpdatedAt: planTestTime,
	}
}

func sampleDetail() plansvc.Detail {
	sess := domain.Session{
		SessionRecord: domain.SessionRecord{ID: "w-1", DisplayName: "Demo · m1: API", Harness: domain.HarnessCodex,
			Metadata: domain.SessionMetadata{Branch: "ao/w-1"}},
		Status: domain.StatusWorking, KanbanColumn: contract.KanbanBuilding, DisplayStatus: contract.DisplayWorking,
		PRs: []domain.PRFacts{{URL: "https://github.com/o/r/pull/7", Number: 7}},
	}
	return plansvc.Detail{
		Summary: plansvc.Summary{
			Plan: samplePlanDomain(), Phase: domain.PlanPhaseImplementing, Kanban: contract.KanbanBuilding,
			DisplayStatus: "Implementing 0/1", Counts: plansvc.MilestoneCounts{Total: 2, Running: 1},
			LiveSessions: []domain.Session{sess}, PRs: sess.PRs,
		},
		Milestones: []plansvc.MilestoneView{{Milestone: sampleMilestoneDomain(), Session: &sess}},
		Events: []domain.PlanEvent{
			{ID: 2, PlanID: "plan_1", MilestoneID: "ms_1", SessionID: "w-1", Kind: "milestone_started", CreatedAt: planTestTime},
			{ID: 1, PlanID: "plan_1", Kind: "plan_created", CreatedAt: planTestTime},
		},
	}
}

func TestPlansAPI_CreateReturns201WithPlan(t *testing.T) {
	p := samplePlanDomain()
	p.State = domain.PlanStatePlanning
	svc := &fakePlanService{plan: p}
	srv := newPlanTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects/mer/plans",
		`{"title":"Demo","brief":"Add X","ticketRef":"github:o/r#1","maxParallel":2}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d body=%s", status, body)
	}
	if in := svc.gotCreate; in.ProjectID != "mer" || in.Title != "Demo" || in.Brief != "Add X" || in.TicketRef != "github:o/r#1" ||
		in.MaxParallel == nil || *in.MaxParallel != 2 {
		t.Fatalf("create input = %+v", svc.gotCreate)
	}
	var resp struct {
		Plan struct {
			ID, State, Phase, KanbanColumn, DisplayStatus, Brief string
			OrchestratorSessionID                                string
		} `json:"plan"`
	}
	mustJSON(t, body, &resp)
	if resp.Plan.ID != "plan_1" || resp.Plan.State != "planning" || resp.Plan.Phase != "planning" ||
		resp.Plan.KanbanColumn != "building" || resp.Plan.DisplayStatus != "Planning" || resp.Plan.Brief != "Add X" ||
		resp.Plan.OrchestratorSessionID != "orch-1" {
		t.Fatalf("plan = %+v", resp.Plan)
	}
}

func TestPlansAPI_ListAndGet(t *testing.T) {
	svc := &fakePlanService{summaries: []plansvc.Summary{sampleDetail().Summary}, detail: sampleDetail()}
	srv := newPlanTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/mer/plans", "")
	if status != http.StatusOK || svc.gotProject != "mer" {
		t.Fatalf("list status=%d project=%q body=%s", status, svc.gotProject, body)
	}
	var list controllers.ListPlansResponse
	mustJSON(t, body, &list)
	if len(list.Plans) == 1 && (len(list.Plans[0].PRs) != 1 || list.Plans[0].PRs[0].Number != 7) {
		t.Fatalf("plan PRs = %+v", list.Plans[0].PRs)
	}
	if len(list.Plans) != 1 || list.Plans[0].DisplayStatus != "Implementing 0/1" || list.Plans[0].MilestoneCounts.Total != 2 ||
		len(list.Plans[0].LiveSessions) != 1 || list.Plans[0].LiveSessions[0].Harness != "codex" ||
		list.Plans[0].LiveSessions[0].KanbanColumn != "building" || list.Plans[0].LiveSessions[0].Status != "working" {
		t.Fatalf("list = %+v", list)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/plans/plan%5F1", "")
	if status != http.StatusOK || svc.gotPlan != "plan_1" {
		t.Fatalf("get status=%d plan=%q body=%s", status, svc.gotPlan, body)
	}
	var detail controllers.PlanDetailResponse
	mustJSON(t, body, &detail)
	if len(detail.Milestones) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	m := detail.Milestones[0]
	if m.Key != "m1" || m.Branch != "ao/w-1" || m.Session == nil || m.Session.ID != "w-1" || len(m.PRs) != 1 ||
		m.PRs[0].Number != 7 || m.SparsePaths[0] != "src/api" || m.DependsOn == nil {
		t.Fatalf("milestone = %+v", m)
	}
	if len(detail.Events) != 2 || detail.Events[0].Kind != "milestone_started" || detail.Events[0].MilestoneKey != "m1" ||
		detail.Events[1].MilestoneKey != "" {
		t.Fatalf("events = %+v", detail.Events)
	}
}

func TestPlansAPI_PatchPlan(t *testing.T) {
	svc := &fakePlanService{summary: sampleDetail().Summary}
	srv := newPlanTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/plans/plan_1", `{"maxParallel":5,"specMarkdown":"# P"}`)
	if status != http.StatusOK || svc.gotPatch.MaxParallel == nil || *svc.gotPatch.MaxParallel != 5 ||
		svc.gotPatch.SpecMarkdown == nil || *svc.gotPatch.SpecMarkdown != "# P" || svc.gotPatch.Title != nil {
		t.Fatalf("status=%d patch=%+v body=%s", status, svc.gotPatch, body)
	}
}

func TestPlansAPI_MilestoneRoutes(t *testing.T) {
	svc := &fakePlanService{milestone: sampleMilestoneDomain(), detail: sampleDetail()}
	srv := newPlanTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/plans/plan_1/milestones",
		`{"key":"m1","title":"API","description":"build it","dependsOn":["m0"],"sparsePaths":["src/api"]}`)
	if status != http.StatusCreated {
		t.Fatalf("add status=%d body=%s", status, body)
	}
	if svc.gotMsIn.Key != "m1" || svc.gotMsIn.Kind != "" || svc.gotMsIn.DependsOn[0] != "m0" || svc.gotMsIn.SparsePaths[0] != "src/api" {
		t.Fatalf("milestone input = %+v", svc.gotMsIn)
	}
	var env controllers.MilestoneEnvelope
	mustJSON(t, body, &env)
	if env.Milestone.Key != "m1" || env.Milestone.Status != "running" {
		t.Fatalf("envelope = %+v", env)
	}

	_, status, _ = doRequest(t, srv, "PATCH", "/api/v1/plans/plan_1/milestones/m1", `{"dependsOn":[]}`)
	if status != http.StatusOK || svc.gotKey != "m1" || svc.gotMsPatch.DependsOn == nil || len(*svc.gotMsPatch.DependsOn) != 0 ||
		svc.gotMsPatch.Title != nil {
		t.Fatalf("update status=%d patch=%+v", status, svc.gotMsPatch)
	}

	_, status, _ = doRequest(t, srv, "DELETE", "/api/v1/plans/plan_1/milestones/m%2D1", "")
	if status != http.StatusNoContent || svc.gotKey != "m-1" {
		t.Fatalf("delete status=%d key=%q", status, svc.gotKey)
	}

	_, status, _ = doRequest(t, srv, "POST", "/api/v1/plans/plan_1/milestones/m1/retry", "")
	if status != http.StatusOK || svc.calls[len(svc.calls)-1] != "retry" {
		t.Fatalf("retry status=%d calls=%v", status, svc.calls)
	}
}

func TestPlansAPI_ActivateCancelQAResultEvents(t *testing.T) {
	svc := &fakePlanService{detail: sampleDetail(), summary: sampleDetail().Summary, events: plansvc.EventsPage{
		Events:        []domain.PlanEvent{{ID: 5, MilestoneID: "ms_1", Kind: "milestone_done", CreatedAt: planTestTime}},
		NextAfter:     5,
		MilestoneKeys: map[domain.MilestoneID]string{"ms_1": "m1"},
	}}
	srv := newPlanTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/plans/plan_1/activate", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"milestones"`) {
		t.Fatalf("activate status=%d body=%s", status, body)
	}
	_, status, _ = doRequest(t, srv, "POST", "/api/v1/plans/plan_1/cancel", "")
	if status != http.StatusOK || svc.calls[len(svc.calls)-1] != "cancel" {
		t.Fatalf("cancel status=%d", status)
	}
	_, status, _ = doRequest(t, srv, "POST", "/api/v1/plans/plan_1/qa-result",
		`{"verdict":"fail","milestoneKey":"m1","note":"broken","sessionId":"qa-1"}`)
	if status != http.StatusOK || svc.gotQA != (plansvc.QAResultInput{Verdict: "fail", MilestoneKey: "m1", Note: "broken", SessionID: "qa-1"}) {
		t.Fatalf("qa status=%d input=%+v", status, svc.gotQA)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/plans/plan_1/events?after=4&limit=10", "")
	if status != http.StatusOK || svc.gotAfter != 4 || svc.gotLimit != 10 {
		t.Fatalf("events status=%d after=%d limit=%d", status, svc.gotAfter, svc.gotLimit)
	}
	var page controllers.PlanEventsResponse
	mustJSON(t, body, &page)
	if page.NextAfter != 5 || len(page.Events) != 1 || page.Events[0].MilestoneKey != "m1" {
		t.Fatalf("page = %+v", page)
	}
	_, status, _ = doRequest(t, srv, "GET", "/api/v1/plans/plan_1/events?after=x", "")
	if status != http.StatusBadRequest {
		t.Fatalf("bad after status=%d", status)
	}
}

func TestPlansAPI_ErrorEnvelopes(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		err                      error
		status                   int
		code                     string
		field                    string
	}{
		{"not found", "GET", "/api/v1/plans/plan_x", "", apierr.NotFound("PLAN_NOT_FOUND", "Unknown plan"), 404, "PLAN_NOT_FOUND", ""},
		{"not editable", "POST", "/api/v1/plans/plan_1/milestones", `{"key":"m1","title":"t","description":"d"}`,
			apierr.Conflict("PLAN_NOT_EDITABLE", "no", nil), 409, "PLAN_NOT_EDITABLE", ""},
		{"invalid milestone", "POST", "/api/v1/plans/plan_1/milestones", `{"key":"Bad","title":"t","description":"d"}`,
			apierr.Invalid("INVALID_MILESTONE", "bad key", map[string]any{"field": "key"}), 400, "INVALID_MILESTONE", "key"},
		{"graph invalid", "POST", "/api/v1/plans/plan_1/activate", "",
			apierr.Invalid("PLAN_GRAPH_INVALID", "dependency cycle: m1 -> m2 -> m1", nil), 400, "PLAN_GRAPH_INVALID", ""},
		{"qa forbidden", "POST", "/api/v1/plans/plan_1/qa-result", `{"verdict":"pass","note":"ok","sessionId":"w"}`,
			apierr.Forbidden("PLAN_QA_SESSION_REQUIRED", "no"), 403, "PLAN_QA_SESSION_REQUIRED", ""},
		{"bad json", "POST", "/api/v1/projects/mer/plans", `{"title":`, nil, 400, "INVALID_JSON", ""},
		{"bad kind", "POST", "/api/v1/plans/plan_1/milestones", `{"key":"m1","kind":"deploy","title":"t","description":"d"}`,
			nil, 400, "INVALID_MILESTONE", "kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakePlanService{err: tc.err, detail: sampleDetail(), milestone: sampleMilestoneDomain()}
			srv := newPlanTestServer(t, svc)
			body, status, _ := doRequest(t, srv, tc.method, tc.path, tc.body)
			if status != tc.status {
				t.Fatalf("status = %d want %d body=%s", status, tc.status, body)
			}
			var env struct {
				Code      string         `json:"code"`
				RequestID string         `json:"requestId"`
				Details   map[string]any `json:"details"`
			}
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}
			if env.Code != tc.code {
				t.Fatalf("code = %q want %q body=%s", env.Code, tc.code, body)
			}
			if tc.field != "" && env.Details["field"] != tc.field {
				t.Fatalf("details = %+v", env.Details)
			}
		})
	}
}

func TestPlansAPI_NotImplementedWithoutService(t *testing.T) {
	srv := newPlanTestServer(t, nil)
	_, status, _ := doRequest(t, srv, "GET", "/api/v1/plans/plan_1", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d", status)
	}
}

func TestPlansAPI_PatchAcceptsLargeSpec(t *testing.T) {
	svc := &fakePlanService{summary: sampleDetail().Summary}
	srv := newPlanTestServer(t, svc)
	spec := strings.Repeat("<li>a & b</li>\n", 200<<10/16)
	raw, _ := json.Marshal(map[string]string{"specMarkdown": spec})
	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/plans/plan_1", string(raw))
	if status != http.StatusOK || svc.gotPatch.SpecMarkdown == nil || len(*svc.gotPatch.SpecMarkdown) != len(spec) {
		t.Fatalf("status=%d body=%.200s", status, body)
	}
	var env controllers.PlanEnvelope
	mustJSON(t, body, &env)
	if env.Plan.DisplayStatus != "Implementing 0/1" || env.Plan.MilestoneCounts.Total != 2 || len(env.Plan.LiveSessions) != 1 {
		t.Fatalf("patch response derived fields = %+v", env.Plan.PlanSummary)
	}
}

func TestPlansAPI_CreateMaxParallelPointerAndUnknownFields(t *testing.T) {
	p := samplePlanDomain()
	svc := &fakePlanService{plan: p}
	srv := newPlanTestServer(t, svc)
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/projects/mer/plans", `{"title":"t","brief":"b","maxParallel":0}`); status != http.StatusCreated ||
		svc.gotCreate.MaxParallel == nil || *svc.gotCreate.MaxParallel != 0 {
		t.Fatalf("explicit 0 must reach the service: status=%d in=%+v", status, svc.gotCreate)
	}
	if _, status, _ := doRequest(t, srv, "POST", "/api/v1/projects/mer/plans", `{"title":"t","brief":"b"}`); status != http.StatusCreated ||
		svc.gotCreate.MaxParallel != nil {
		t.Fatalf("omitted maxParallel must be nil: status=%d in=%+v", status, svc.gotCreate)
	}
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/plans/plan_1/milestones", `{"key":"m1","title":"t","description":"d","dependencies":["x"]}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if !strings.Contains(string(body), "dependencies") {
		t.Fatalf("unknown field not named: %s", body)
	}
}
