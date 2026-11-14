package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	plansvc "github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
)

// PlanService is the controller-facing task plan contract.
type PlanService interface {
	Create(ctx context.Context, in plansvc.CreateInput) (domain.Plan, error)
	Get(ctx context.Context, id domain.PlanID) (plansvc.Detail, error)
	List(ctx context.Context, projectID domain.ProjectID) ([]plansvc.Summary, error)
	Update(ctx context.Context, id domain.PlanID, patch plansvc.PlanPatch) (domain.Plan, error)
	AddMilestone(ctx context.Context, id domain.PlanID, in plansvc.MilestoneInput) (domain.Milestone, error)
	UpdateMilestone(ctx context.Context, id domain.PlanID, key string, patch plansvc.MilestonePatch) (domain.Milestone, error)
	RemoveMilestone(ctx context.Context, id domain.PlanID, key string) error
	Activate(ctx context.Context, id domain.PlanID) (plansvc.Detail, error)
	Cancel(ctx context.Context, id domain.PlanID) (domain.Plan, error)
	RetryMilestone(ctx context.Context, id domain.PlanID, key string) (plansvc.Detail, error)
	RecordQAResult(ctx context.Context, id domain.PlanID, in plansvc.QAResultInput) (plansvc.Detail, error)
	Events(ctx context.Context, id domain.PlanID, after int64, limit int) (plansvc.EventsPage, error)
}

// PlansController owns the task plan routes: a ticket planned by the project
// orchestrator into milestones that AO runs as workers and verifies with QA.
type PlansController struct {
	Svc PlanService
}

const planBodyLimit = 128 << 10

// Register mounts the plan REST routes.
func (c *PlansController) Register(r chi.Router) {
	r.Post("/projects/{projectId}/plans", c.create)
	r.Get("/projects/{projectId}/plans", c.list)
	r.Get("/plans/{planId}", c.get)
	r.Patch("/plans/{planId}", c.patch)
	r.Post("/plans/{planId}/milestones", c.addMilestone)
	r.Patch("/plans/{planId}/milestones/{key}", c.updateMilestone)
	r.Delete("/plans/{planId}/milestones/{key}", c.removeMilestone)
	r.Post("/plans/{planId}/milestones/{key}/retry", c.retryMilestone)
	r.Post("/plans/{planId}/activate", c.activate)
	r.Post("/plans/{planId}/cancel", c.cancel)
	r.Post("/plans/{planId}/qa-result", c.qaResult)
	r.Get("/plans/{planId}/events", c.events)
}

func (c *PlansController) create(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{projectId}/plans")
		return
	}
	var req CreatePlanRequest
	if !decodePlanBody(w, r, &req) {
		return
	}
	projectID, ok := planPathParam(w, r, "projectId", "PROJECT_ID_INVALID", "Invalid project id")
	if !ok {
		return
	}
	p, err := c.Svc.Create(r.Context(), plansvc.CreateInput{
		ProjectID: domain.ProjectID(projectID), Title: req.Title, Brief: req.Brief,
		TicketRef: req.TicketRef, MaxParallel: req.MaxParallel,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, PlanEnvelope{Plan: planResponseFromPlan(p, nil)})
}

func (c *PlansController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{projectId}/plans")
		return
	}
	projectID, ok := planPathParam(w, r, "projectId", "PROJECT_ID_INVALID", "Invalid project id")
	if !ok {
		return
	}
	summaries, err := c.Svc.List(r.Context(), domain.ProjectID(projectID))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]PlanSummary, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, planSummary(s))
	}
	envelope.WriteJSON(w, http.StatusOK, ListPlansResponse{Plans: out})
}

func (c *PlansController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/plans/{planId}")
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	d, err := c.Svc.Get(r.Context(), id)
	writePlanDetail(w, r, d, err)
}

func (c *PlansController) patch(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/plans/{planId}")
		return
	}
	var req PlanPatchRequest
	if !decodePlanBody(w, r, &req) {
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	p, err := c.Svc.Update(r.Context(), id, plansvc.PlanPatch{Title: req.Title, MaxParallel: req.MaxParallel, SpecMarkdown: req.SpecMarkdown})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PlanEnvelope{Plan: planResponseFromPlan(p, nil)})
}

func (c *PlansController) addMilestone(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/plans/{planId}/milestones")
		return
	}
	var req MilestoneRequest
	if !decodePlanBody(w, r, &req) {
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	patch, ok := milestonePatch(w, r, req)
	if !ok {
		return
	}
	in := plansvc.MilestoneInput{}
	if patch.Key != nil {
		in.Key = *patch.Key
	}
	if patch.Kind != nil {
		in.Kind = *patch.Kind
	}
	if patch.Title != nil {
		in.Title = *patch.Title
	}
	if patch.Description != nil {
		in.Description = *patch.Description
	}
	if patch.DependsOn != nil {
		in.DependsOn = *patch.DependsOn
	}
	if patch.SparsePaths != nil {
		in.SparsePaths = *patch.SparsePaths
	}
	m, err := c.Svc.AddMilestone(r.Context(), id, in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, MilestoneEnvelope{Milestone: milestoneResponse(plansvc.MilestoneView{Milestone: m})})
}

func (c *PlansController) updateMilestone(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/plans/{planId}/milestones/{key}")
		return
	}
	var req MilestoneRequest
	if !decodePlanBody(w, r, &req) {
		return
	}
	id, key, ok := planMilestoneParams(w, r)
	if !ok {
		return
	}
	patch, ok := milestonePatch(w, r, req)
	if !ok {
		return
	}
	m, err := c.Svc.UpdateMilestone(r.Context(), id, key, patch)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MilestoneEnvelope{Milestone: milestoneResponse(plansvc.MilestoneView{Milestone: m})})
}

func (c *PlansController) removeMilestone(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/plans/{planId}/milestones/{key}")
		return
	}
	id, key, ok := planMilestoneParams(w, r)
	if !ok {
		return
	}
	if err := c.Svc.RemoveMilestone(r.Context(), id, key); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *PlansController) retryMilestone(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/plans/{planId}/milestones/{key}/retry")
		return
	}
	id, key, ok := planMilestoneParams(w, r)
	if !ok {
		return
	}
	d, err := c.Svc.RetryMilestone(r.Context(), id, key)
	writePlanDetail(w, r, d, err)
}

func (c *PlansController) activate(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/plans/{planId}/activate")
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	d, err := c.Svc.Activate(r.Context(), id)
	writePlanDetail(w, r, d, err)
}

func (c *PlansController) cancel(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/plans/{planId}/cancel")
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	p, err := c.Svc.Cancel(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PlanEnvelope{Plan: planResponseFromPlan(p, nil)})
}

func (c *PlansController) qaResult(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/plans/{planId}/qa-result")
		return
	}
	var req QAResultRequest
	if !decodePlanBody(w, r, &req) {
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	d, err := c.Svc.RecordQAResult(r.Context(), id, plansvc.QAResultInput{
		Verdict: domain.QAVerdict(req.Verdict), MilestoneKey: req.MilestoneKey, Note: req.Note,
		SessionID: domain.SessionID(req.SessionID),
	})
	writePlanDetail(w, r, d, err)
}

func (c *PlansController) events(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/plans/{planId}/events")
		return
	}
	id, ok := planIDParam(w, r)
	if !ok {
		return
	}
	var after int64
	limit := 0
	if raw := r.URL.Query().Get("after"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "after must be a non-negative integer", map[string]any{"field": "after"})
			return
		}
		after = v
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > plansvc.MaxEventsPage {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", "limit must be 1-500", map[string]any{"field": "limit"})
			return
		}
		limit = v
	}
	page, err := c.Svc.Events(r.Context(), id, after, limit)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PlanEventsResponse{
		Events:    planEventResponses(page.Events, page.MilestoneKeys),
		NextAfter: page.NextAfter,
	})
}

func writePlanDetail(w http.ResponseWriter, r *http.Request, d plansvc.Detail, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	keys := make(map[domain.MilestoneID]string, len(d.Milestones))
	milestones := make([]MilestoneResponse, 0, len(d.Milestones))
	for _, v := range d.Milestones {
		keys[v.Milestone.ID] = v.Milestone.Key
		milestones = append(milestones, milestoneResponse(v))
	}
	envelope.WriteJSON(w, http.StatusOK, PlanDetailResponse{
		Plan:       planResponse(d.Summary),
		Milestones: milestones,
		Events:     planEventResponses(d.Events, keys),
	})
}

func decodePlanBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, planBodyLimit))
	err := decoder.Decode(dst)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) == nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Expected a single JSON body", nil)
			return false
		}
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "bad_request", "PLAN_BODY_TOO_LARGE", "Plan request body is too large", nil)
	} else {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
	}
	return false
}

func planPathParam(w http.ResponseWriter, r *http.Request, name, code, message string) (string, bool) {
	v, err := url.PathUnescape(chi.URLParam(r, name))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", code, message, nil)
		return "", false
	}
	return v, true
}

func planIDParam(w http.ResponseWriter, r *http.Request) (domain.PlanID, bool) {
	v, ok := planPathParam(w, r, "planId", "PLAN_ID_INVALID", "Invalid plan id")
	return domain.PlanID(v), ok
}

func planMilestoneParams(w http.ResponseWriter, r *http.Request) (domain.PlanID, string, bool) {
	id, ok := planIDParam(w, r)
	if !ok {
		return "", "", false
	}
	key, ok := planPathParam(w, r, "key", "MILESTONE_KEY_INVALID", "Invalid milestone key")
	return id, key, ok
}

func milestonePatch(w http.ResponseWriter, r *http.Request, req MilestoneRequest) (plansvc.MilestonePatch, bool) {
	patch := plansvc.MilestonePatch{
		Key: req.Key, Title: req.Title, Description: req.Description,
		DependsOn: req.DependsOn, SparsePaths: req.SparsePaths,
	}
	if req.Kind != nil {
		kind := domain.MilestoneKind(*req.Kind)
		if kind != domain.MilestoneKindImplement && kind != domain.MilestoneKindQA {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_MILESTONE", "kind must be implement or qa", map[string]any{"field": "kind"})
			return plansvc.MilestonePatch{}, false
		}
		patch.Kind = &kind
	}
	return patch, true
}

// planResponseFromPlan renders a plan whose milestones the caller did not
// load; the derived fields reflect the plan alone (planning/cancelled are
// exact, an active plan reads as implementing until re-read).
func planResponseFromPlan(p domain.Plan, ms []domain.Milestone) PlanResponse {
	views := make([]plansvc.MilestoneView, 0, len(ms))
	for _, m := range ms {
		views = append(views, plansvc.MilestoneView{Milestone: m})
	}
	phase := domain.DerivePlanPhase(p, ms)
	col, display := domain.DerivePlanKanban(phase, ms)
	return planResponse(plansvc.Summary{Plan: p, Phase: phase, Kanban: col, DisplayStatus: display, LiveSessions: []domain.Session{}})
}

func planResponse(s plansvc.Summary) PlanResponse {
	return PlanResponse{PlanSummary: planSummary(s), Brief: s.Plan.Brief, SpecMarkdown: s.Plan.SpecMarkdown}
}

func planSummary(s plansvc.Summary) PlanSummary {
	live := make([]PlanSessionSummary, 0, len(s.LiveSessions))
	for _, sess := range s.LiveSessions {
		live = append(live, planSessionSummary(sess))
	}
	p := s.Plan
	return PlanSummary{
		ID:            string(p.ID),
		ProjectID:     string(p.ProjectID),
		Title:         p.Title,
		TicketRef:     p.TicketRef,
		State:         string(p.State),
		Phase:         string(s.Phase),
		KanbanColumn:  string(s.Kanban),
		DisplayStatus: string(s.DisplayStatus),
		MilestoneCounts: PlanMilestoneCounts{
			Total: s.Counts.Total, Done: s.Counts.Done, Running: s.Counts.Running,
			Failed: s.Counts.Failed, Blocked: s.Counts.Blocked,
		},
		LiveSessions:          live,
		OrchestratorSessionID: string(p.OrchestratorSessionID),
		MaxParallel:           p.MaxParallel,
		CreatedAt:             p.CreatedAt,
		UpdatedAt:             p.UpdatedAt,
		ActivatedAt:           p.ActivatedAt,
		FinishedAt:            p.FinishedAt,
	}
}

func planSessionSummary(s domain.Session) PlanSessionSummary {
	return PlanSessionSummary{
		ID:            string(s.ID),
		DisplayName:   s.DisplayName,
		Harness:       string(s.Harness),
		Status:        string(s.Status),
		KanbanColumn:  string(s.KanbanColumn),
		DisplayStatus: string(s.DisplayStatus),
		IsTerminated:  s.IsTerminated,
	}
}

func milestoneResponse(v plansvc.MilestoneView) MilestoneResponse {
	m := v.Milestone
	resp := MilestoneResponse{
		ID:          string(m.ID),
		Key:         m.Key,
		Ordinal:     m.Ordinal,
		Kind:        string(m.Kind),
		Title:       m.Title,
		Description: m.Description,
		Status:      string(m.Status),
		DependsOn:   append([]string{}, m.DependsOn...),
		SparsePaths: append([]string{}, m.SparsePaths...),
		QAVerdict:   string(m.QAVerdict),
		QANote:      m.QANote,
		Attempt:     m.Attempt,
		PRs:         []SessionPRFacts{},
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
		StartedAt:   m.StartedAt,
		FinishedAt:  m.FinishedAt,
	}
	if v.Session != nil {
		summary := planSessionSummary(*v.Session)
		resp.Session = &summary
		resp.Branch = v.Session.Metadata.Branch
		resp.PRs = sessionPRFacts(v.Session.PRs)
	}
	return resp
}

func planEventResponses(evs []domain.PlanEvent, keys map[domain.MilestoneID]string) []PlanEventResponse {
	out := make([]PlanEventResponse, 0, len(evs))
	for _, e := range evs {
		out = append(out, PlanEventResponse{
			ID: e.ID, MilestoneKey: keys[e.MilestoneID], SessionID: string(e.SessionID),
			Kind: e.Kind, Note: e.Note, CreatedAt: e.CreatedAt,
		})
	}
	return out
}
