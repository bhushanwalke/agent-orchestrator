package plan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plan event kinds (spec §9).
const (
	EventPlanCreated         = "plan_created"
	EventKickoffSent         = "kickoff_sent"
	EventKickoffDeferred     = "kickoff_deferred"
	EventSpecSet             = "spec_set"
	EventMilestoneAdded      = "milestone_added"
	EventMilestoneUpdated    = "milestone_updated"
	EventMilestoneRemoved    = "milestone_removed"
	EventPlanActivated       = "plan_activated"
	EventMilestoneReady      = "milestone_ready"
	EventMilestoneStarted    = "milestone_started"
	EventMilestoneCheckpoint = "milestone_checkpoint"
	EventMilestoneBlocked    = "milestone_blocked"
	EventMilestoneDone       = "milestone_done"
	EventMilestoneFailed     = "milestone_failed"
	EventQAPassed            = "qa_passed"
	EventQAFailed            = "qa_failed"
	EventDefectSent          = "defect_sent"
	EventPlanDone            = "plan_done"
	EventPlanCancelled       = "plan_cancelled"
	EventSparseDisabled      = "sparse_disabled"
)

const (
	// DetailEventLimit is how many recent events a plan detail carries.
	DetailEventLimit = 100
	// MaxEventsPage bounds one events page.
	MaxEventsPage = 500
	// TicketRefMaxBytes bounds the display-only ticket reference.
	TicketRefMaxBytes = 2048
	// SpecMarkdownMaxBytes bounds the orchestrator's plan document.
	SpecMarkdownMaxBytes = 262144
	qaMilestoneTitle     = "QA: verify the whole feature"
)

// Service owns the task plan lifecycle: creation and orchestrator kickoff,
// milestone editing while planning, activation, cancellation, retries, QA
// verdicts, and the read models the API serves.
type Service struct {
	store     Store
	sessions  Sessions
	projects  Projects
	scheduler Scheduler
	now       func() time.Time
	newID     func(prefix string) string
	logger    *slog.Logger
	// mu serialises plan writes so read-modify-write edits never interleave.
	mu sync.Mutex

	baseCtx       context.Context
	retryInterval time.Duration
	retryTimeout  time.Duration
	retryMu       sync.Mutex
	retrying      map[domain.PlanID]bool
}

// New constructs the plan service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.NewID == nil {
		d.NewID = func(prefix string) string { return prefix + uuid.NewString() }
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.BaseContext == nil {
		d.BaseContext = context.Background()
	}
	if d.KickoffRetryInterval == 0 {
		d.KickoffRetryInterval = 5 * time.Second
	}
	if d.KickoffRetryTimeout == 0 {
		d.KickoffRetryTimeout = 5 * time.Minute
	}
	return &Service{
		store: d.Store, sessions: d.Sessions, projects: d.Projects, scheduler: d.Scheduler,
		now: d.Now, newID: d.NewID, logger: d.Logger,
		baseCtx: d.BaseContext, retryInterval: d.KickoffRetryInterval, retryTimeout: d.KickoffRetryTimeout,
		retrying: map[domain.PlanID]bool{},
	}
}

// Create records a new plan in the planning state and asks the project
// orchestrator to plan it. An orchestrator that cannot start or refuses the
// message right now does not fail the request: the plan records
// kickoff_deferred and stays in planning.
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Plan, error) {
	title := strings.TrimSpace(in.Title)
	if err := validateTitle(title); err != nil {
		return domain.Plan{}, err
	}
	if strings.TrimSpace(in.Brief) == "" || len(in.Brief) > domain.PlanBriefMaxBytes {
		return domain.Plan{}, invalidPlan("brief", fmt.Sprintf("Brief is required and must be at most %d bytes", domain.PlanBriefMaxBytes))
	}
	ticket := strings.TrimSpace(in.TicketRef)
	if len(ticket) > TicketRefMaxBytes {
		return domain.Plan{}, invalidPlan("ticketRef", fmt.Sprintf("Ticket reference must be at most %d bytes", TicketRefMaxBytes))
	}
	maxParallel := in.MaxParallel
	if maxParallel == 0 {
		maxParallel = domain.DefaultPlanMaxParallel
	}
	if err := validateMaxParallel(maxParallel); err != nil {
		return domain.Plan{}, err
	}
	project, err := s.requireProject(ctx, in.ProjectID)
	if err != nil {
		return domain.Plan{}, err
	}

	now := s.now().UTC()
	p := domain.Plan{
		ID:          domain.PlanID(s.newID("plan_")),
		ProjectID:   in.ProjectID,
		Title:       title,
		Brief:       in.Brief,
		TicketRef:   ticket,
		State:       domain.PlanStatePlanning,
		MaxParallel: maxParallel,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.InsertPlan(ctx, p); err != nil {
		return domain.Plan{}, storeError(err)
	}
	s.recordEvent(ctx, p.ID, "", "", EventPlanCreated, "")

	orch, err := s.sessions.EnsureOrchestrator(ctx, p.ProjectID)
	if err != nil {
		s.logger.Warn("plan kickoff deferred: orchestrator unavailable", "plan", p.ID, "error", err)
		s.recordEvent(ctx, p.ID, "", "", EventKickoffDeferred, "orchestrator unavailable: "+err.Error())
		s.scheduleKickoffRetry(p.ID)
		return p, nil
	}
	s.mu.Lock()
	p, err = s.mutatePlan(ctx, p.ID, func(p *domain.Plan) error {
		p.OrchestratorSessionID = orch.ID
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return domain.Plan{}, err
	}
	if err := s.deliverKickoff(ctx, p, project); err != nil {
		s.recordEvent(ctx, p.ID, "", p.OrchestratorSessionID, EventKickoffDeferred, err.Error())
		s.scheduleKickoffRetry(p.ID)
	}
	return p, nil
}

// deliverKickoff sends the orchestrator the plan request through the guarded
// path, recording kickoff_sent when it lands.
func (s *Service) deliverKickoff(ctx context.Context, p domain.Plan, project domain.ProjectRecord) error {
	err := s.sessions.Send(ctx, p.OrchestratorSessionID, KickoffMessage(p, projectName(project)), ports.MessageDeliveryOptions{})
	if err != nil {
		s.logger.Info("plan kickoff refused", "plan", p.ID, "error", err)
		return err
	}
	s.recordEvent(ctx, p.ID, "", p.OrchestratorSessionID, EventKickoffSent, "")
	return nil
}

// RetryDeferredKickoff tries once more to deliver a planning plan's kickoff
// that has not landed yet. It reports whether the kickoff is now delivered
// (or no longer needed because the plan left planning).
func (s *Service) RetryDeferredKickoff(ctx context.Context, id domain.PlanID) (bool, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return false, err
	}
	if p.State != domain.PlanStatePlanning {
		return true, nil
	}
	sent, err := s.kickoffSent(ctx, id)
	if err != nil || sent {
		return sent, err
	}
	project, err := s.requireProject(ctx, p.ProjectID)
	if err != nil {
		return false, err
	}
	orch, err := s.sessions.EnsureOrchestrator(ctx, p.ProjectID)
	if err != nil {
		return false, nil
	}
	if orch.ID != p.OrchestratorSessionID {
		s.mu.Lock()
		p, err = s.mutatePlan(ctx, p.ID, func(p *domain.Plan) error {
			p.OrchestratorSessionID = orch.ID
			return nil
		})
		s.mu.Unlock()
		if err != nil {
			return false, err
		}
	}
	return s.deliverKickoff(ctx, p, project) == nil, nil
}

func (s *Service) kickoffSent(ctx context.Context, id domain.PlanID) (bool, error) {
	var after int64
	for {
		evs, err := s.store.ListPlanEvents(ctx, id, after, MaxEventsPage)
		if err != nil {
			return false, err
		}
		for _, e := range evs {
			if e.Kind == EventKickoffSent {
				return true, nil
			}
		}
		if len(evs) < MaxEventsPage {
			return false, nil
		}
		after = evs[len(evs)-1].ID
	}
}

// scheduleKickoffRetry retries a deferred kickoff in the background until it
// lands, the plan leaves planning, or the retry window closes. A freshly
// started orchestrator refuses messages until its startup settles, so the
// first delivery usually needs this.
func (s *Service) scheduleKickoffRetry(id domain.PlanID) {
	if s.retryInterval < 0 {
		return
	}
	s.retryMu.Lock()
	if s.retrying[id] {
		s.retryMu.Unlock()
		return
	}
	s.retrying[id] = true
	s.retryMu.Unlock()
	go func() {
		defer func() {
			s.retryMu.Lock()
			delete(s.retrying, id)
			s.retryMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.baseCtx, s.retryTimeout)
		defer cancel()
		ticker := time.NewTicker(s.retryInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			done, err := s.RetryDeferredKickoff(ctx, id)
			if err != nil {
				s.logger.Warn("plan kickoff retry failed", "plan", id, "error", err)
				return
			}
			if done {
				return
			}
		}
	}()
}

// KickoffMessage is the orchestrator's plan request. The wrapper tag is exact
// so the orchestrator prompt and tests can recognise it.
func KickoffMessage(p domain.Plan, projectName string) string {
	ticket := p.TicketRef
	if ticket == "" {
		ticket = "(none)"
	}
	return fmt.Sprintf(`<ao-plan-request id="%[1]s">
A new Task was created for project %[2]s.

Title: %[3]s
Ticket: %[4]s

%[5]s

Plan it now. Follow the "Plans (Tasks)" section of your standing instructions:
investigate the repository, spawn research workers in parallel if the ticket is broad,
write the plan with `+"`ao plan set-spec %[1]s --file <plan.md>`"+`, register milestones with
`+"`ao plan milestone add %[1]s ...`"+`, then `+"`ao plan activate %[1]s`"+`.
</ao-plan-request>`, p.ID, projectName, p.Title, ticket, strings.TrimSpace(p.Brief))
}

// Get returns one plan with its milestones and recent events.
func (s *Service) Get(ctx context.Context, id domain.PlanID) (Detail, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	return s.detail(ctx, p)
}

// List returns a project's plans newest first; an empty project lists all.
func (s *Service) List(ctx context.Context, projectID domain.ProjectID) ([]Summary, error) {
	plans, err := s.store.ListPlans(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(plans))
	for _, p := range plans {
		ms, err := s.store.ListMilestones(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		views := s.milestoneViews(ctx, ms)
		out = append(out, summarize(p, views))
	}
	return out, nil
}

// Update edits a plan. Title and spec change only while planning; the
// parallel limit stays editable until the plan is cancelled.
func (s *Service) Update(ctx context.Context, id domain.PlanID, patch PlanPatch) (domain.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var specChanged bool
	p, err := s.mutatePlan(ctx, id, func(p *domain.Plan) error {
		if (patch.Title != nil || patch.SpecMarkdown != nil) && p.State != domain.PlanStatePlanning {
			return notEditable()
		}
		if patch.MaxParallel != nil && p.State == domain.PlanStateCancelled {
			return notEditable()
		}
		if patch.Title != nil {
			title := strings.TrimSpace(*patch.Title)
			if err := validateTitle(title); err != nil {
				return err
			}
			p.Title = title
		}
		if patch.MaxParallel != nil {
			if err := validateMaxParallel(*patch.MaxParallel); err != nil {
				return err
			}
			p.MaxParallel = *patch.MaxParallel
		}
		if patch.SpecMarkdown != nil {
			if err := validateSpec(*patch.SpecMarkdown); err != nil {
				return err
			}
			p.SpecMarkdown = *patch.SpecMarkdown
			specChanged = true
		}
		return nil
	})
	if err != nil {
		return domain.Plan{}, err
	}
	if specChanged {
		s.recordEvent(ctx, p.ID, "", "", EventSpecSet, "")
	}
	return p, nil
}

// SetSpec stores the orchestrator's plan document.
func (s *Service) SetSpec(ctx context.Context, id domain.PlanID, markdown string) (domain.Plan, error) {
	return s.Update(ctx, id, PlanPatch{SpecMarkdown: &markdown})
}

// AddMilestone appends a milestone to a plan that is still planning.
// Dependencies must name milestones that already exist; cycles are only
// rejected at activation so milestones can be rewired in any order.
func (s *Service) AddMilestone(ctx context.Context, id domain.PlanID, in MilestoneInput) (domain.Milestone, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.requireEditable(ctx, id)
	if err != nil {
		return domain.Milestone{}, err
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return domain.Milestone{}, err
	}
	if len(ms) >= domain.MaxPlanMilestones {
		return domain.Milestone{}, apierr.Invalid("PLAN_TOO_MANY_MILESTONES",
			fmt.Sprintf("A plan has at most %d milestones", domain.MaxPlanMilestones), nil)
	}
	now := s.now().UTC()
	kind := in.Kind
	if kind == "" {
		kind = domain.MilestoneKindImplement
	}
	m := domain.Milestone{
		ID:          domain.MilestoneID(s.newID("ms_")),
		PlanID:      p.ID,
		Key:         strings.TrimSpace(in.Key),
		Ordinal:     nextOrdinal(ms),
		Kind:        kind,
		Title:       strings.TrimSpace(in.Title),
		Description: in.Description,
		Status:      domain.MilestoneStatusPending,
		SparsePaths: normalizePaths(in.SparsePaths),
		DependsOn:   normalizeKeys(in.DependsOn),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := validateMilestoneInPlan(m, ms); err != nil {
		return domain.Milestone{}, err
	}
	if err := s.store.InsertMilestone(ctx, m); err != nil {
		return domain.Milestone{}, storeError(err)
	}
	if err := s.store.SetMilestoneDeps(ctx, m.ID, depIDs(m.DependsOn, ms)); err != nil {
		return domain.Milestone{}, err
	}
	s.recordEvent(ctx, p.ID, m.ID, "", EventMilestoneAdded, m.Key)
	s.touchPlan(ctx, p.ID)
	return m, nil
}

// UpdateMilestone edits a milestone of a plan that is still planning.
func (s *Service) UpdateMilestone(ctx context.Context, id domain.PlanID, key string, patch MilestonePatch) (domain.Milestone, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.requireEditable(ctx, id)
	if err != nil {
		return domain.Milestone{}, err
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return domain.Milestone{}, err
	}
	idx := indexOfKey(ms, key)
	if idx < 0 {
		return domain.Milestone{}, milestoneNotFound()
	}
	m := ms[idx]
	oldKey := m.Key
	if patch.Key != nil {
		m.Key = strings.TrimSpace(*patch.Key)
	}
	if patch.Kind != nil {
		m.Kind = *patch.Kind
	}
	if patch.Title != nil {
		m.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Description != nil {
		m.Description = *patch.Description
	}
	if patch.DependsOn != nil {
		m.DependsOn = normalizeKeys(*patch.DependsOn)
	}
	if patch.SparsePaths != nil {
		m.SparsePaths = normalizePaths(*patch.SparsePaths)
	}
	others := append(append([]domain.Milestone{}, ms[:idx]...), ms[idx+1:]...)
	if m.Key != oldKey {
		for _, o := range others {
			for _, dep := range o.DependsOn {
				if dep == oldKey {
					return domain.Milestone{}, apierr.Conflict("MILESTONE_HAS_DEPENDENTS",
						fmt.Sprintf("Milestone %s is depended on by %s; update that dependency first", oldKey, o.Key),
						map[string]any{"dependents": []string{o.Key}})
				}
			}
		}
	}
	if err := validateMilestoneInPlan(m, others); err != nil {
		return domain.Milestone{}, err
	}
	m.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateMilestone(ctx, m); err != nil {
		return domain.Milestone{}, storeError(err)
	}
	if patch.DependsOn != nil {
		if err := s.store.SetMilestoneDeps(ctx, m.ID, depIDs(m.DependsOn, others)); err != nil {
			return domain.Milestone{}, err
		}
	}
	s.recordEvent(ctx, p.ID, m.ID, "", EventMilestoneUpdated, m.Key)
	s.touchPlan(ctx, p.ID)
	return m, nil
}

// RemoveMilestone deletes a milestone of a plan that is still planning. A
// milestone another one depends on is refused rather than silently unwired.
func (s *Service) RemoveMilestone(ctx context.Context, id domain.PlanID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.requireEditable(ctx, id)
	if err != nil {
		return err
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return err
	}
	idx := indexOfKey(ms, key)
	if idx < 0 {
		return milestoneNotFound()
	}
	var dependents []string
	for _, o := range ms {
		for _, dep := range o.DependsOn {
			if dep == key {
				dependents = append(dependents, o.Key)
			}
		}
	}
	if len(dependents) > 0 {
		return apierr.Conflict("MILESTONE_HAS_DEPENDENTS",
			fmt.Sprintf("Milestone %s is depended on by %s; remove those dependencies first", key, strings.Join(dependents, ", ")),
			map[string]any{"dependents": dependents})
	}
	if err := s.store.DeleteMilestone(ctx, ms[idx].ID); err != nil {
		return err
	}
	s.recordEvent(ctx, p.ID, "", "", EventMilestoneRemoved, key)
	s.touchPlan(ctx, p.ID)
	return nil
}

// Activate validates the milestone graph, adds the QA milestone when the
// planner did not, and starts the plan. The scheduler (when present) runs
// once before returning so the first workers start immediately.
func (s *Service) Activate(ctx context.Context, id domain.PlanID) (Detail, error) {
	s.mu.Lock()
	p, err := s.activateLocked(ctx, id)
	s.mu.Unlock()
	if err != nil {
		return Detail{}, err
	}
	s.tick(ctx)
	p, err = s.requirePlan(ctx, p.ID)
	if err != nil {
		return Detail{}, err
	}
	return s.detail(ctx, p)
}

func (s *Service) activateLocked(ctx context.Context, id domain.PlanID) (domain.Plan, error) {
	p, err := s.requireEditable(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return domain.Plan{}, err
	}
	now := s.now().UTC()
	candidate := ms
	var qa *domain.Milestone
	if indexOfKind(ms, domain.MilestoneKindQA) < 0 {
		qa = &domain.Milestone{
			ID:          domain.MilestoneID(s.newID("ms_")),
			PlanID:      p.ID,
			Key:         domain.QAMilestoneKey,
			Ordinal:     nextOrdinal(ms),
			Kind:        domain.MilestoneKindQA,
			Title:       qaMilestoneTitle,
			Status:      domain.MilestoneStatusPending,
			SparsePaths: []string{},
			DependsOn:   []string{},
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		candidate = append(append([]domain.Milestone{}, ms...), *qa)
	}
	if err := domain.ValidatePlanActivation(candidate); err != nil {
		return domain.Plan{}, graphInvalid(err)
	}
	if qa != nil {
		if err := s.store.InsertMilestone(ctx, *qa); err != nil {
			return domain.Plan{}, storeError(err)
		}
		s.recordEvent(ctx, p.ID, qa.ID, "", EventMilestoneAdded, qa.Key)
	}
	for _, m := range ms {
		if m.Status == domain.MilestoneStatusPending {
			continue
		}
		m.Status = domain.MilestoneStatusPending
		m.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, m); err != nil {
			return domain.Plan{}, err
		}
	}
	p, err = s.mutatePlan(ctx, p.ID, func(p *domain.Plan) error {
		p.State = domain.PlanStateActive
		p.ActivatedAt = &now
		return nil
	})
	if err != nil {
		return domain.Plan{}, err
	}
	s.recordEvent(ctx, p.ID, "", "", EventPlanActivated, "")
	return p, nil
}

// Cancel stops a plan. Open milestones become cancelled; sessions are left
// running so the person can still use them. Cancelling twice is a no-op.
func (s *Service) Cancel(ctx context.Context, id domain.PlanID) (domain.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if p.State == domain.PlanStateCancelled {
		return p, nil
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return domain.Plan{}, err
	}
	now := s.now().UTC()
	for _, m := range ms {
		switch m.Status {
		case domain.MilestoneStatusDone, domain.MilestoneStatusFailed, domain.MilestoneStatusCancelled:
			continue
		}
		m.Status = domain.MilestoneStatusCancelled
		m.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, m); err != nil {
			return domain.Plan{}, err
		}
	}
	p, err = s.mutatePlan(ctx, p.ID, func(p *domain.Plan) error {
		p.State = domain.PlanStateCancelled
		return nil
	})
	if err != nil {
		return domain.Plan{}, err
	}
	s.recordEvent(ctx, p.ID, "", "", EventPlanCancelled, "")
	return p, nil
}

// RetryMilestone is the human reset for a failed milestone: it becomes ready
// with a fresh attempt budget and the scheduler is woken.
func (s *Service) RetryMilestone(ctx context.Context, id domain.PlanID, key string) (Detail, error) {
	s.mu.Lock()
	p, err := s.retryLocked(ctx, id, key)
	s.mu.Unlock()
	if err != nil {
		return Detail{}, err
	}
	s.tick(ctx)
	p, err = s.requirePlan(ctx, p.ID)
	if err != nil {
		return Detail{}, err
	}
	return s.detail(ctx, p)
}

func (s *Service) retryLocked(ctx context.Context, id domain.PlanID, key string) (domain.Plan, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return domain.Plan{}, err
	}
	idx := indexOfKey(ms, key)
	if idx < 0 {
		return domain.Plan{}, milestoneNotFound()
	}
	m := ms[idx]
	if p.State != domain.PlanStateActive || m.Status != domain.MilestoneStatusFailed {
		return domain.Plan{}, apierr.Conflict("MILESTONE_NOT_RETRYABLE", "Only a failed milestone of an active plan can be retried", nil)
	}
	m.Status = domain.MilestoneStatusReady
	m.Attempt = 0
	m.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateMilestone(ctx, m); err != nil {
		return domain.Plan{}, err
	}
	s.recordEvent(ctx, p.ID, m.ID, "", EventMilestoneReady, "retry requested")
	return p, nil
}

// RecordQAResult records the QA worker's verdict. Only the session that owns
// the plan's QA milestone may call it. A pass completes the QA milestone; a
// fail against an implement milestone sends that milestone back to ready with
// the defect note and returns QA to pending until the fixes are done; a fail
// with no milestone blocks QA and forwards the note to the orchestrator.
func (s *Service) RecordQAResult(ctx context.Context, id domain.PlanID, in QAResultInput) (Detail, error) {
	if in.Verdict != domain.QAVerdictPass && in.Verdict != domain.QAVerdictFail {
		return Detail{}, apierr.Invalid("INVALID_QA_RESULT", "Verdict must be pass or fail", map[string]any{"field": "verdict"})
	}
	if len(in.Note) > domain.QANoteMaxBytes {
		return Detail{}, apierr.Invalid("INVALID_QA_RESULT", fmt.Sprintf("Note must be at most %d bytes", domain.QANoteMaxBytes), map[string]any{"field": "note"})
	}
	if in.Verdict == domain.QAVerdictPass && in.MilestoneKey != "" {
		return Detail{}, apierr.Invalid("INVALID_QA_RESULT", "A pass verdict covers the whole plan; omit the milestone", map[string]any{"field": "milestoneKey"})
	}
	s.mu.Lock()
	p, forward, err := s.qaResultLocked(ctx, id, in)
	s.mu.Unlock()
	if err != nil {
		return Detail{}, err
	}
	if forward != "" && p.OrchestratorSessionID != "" {
		if err := s.sessions.Send(ctx, p.OrchestratorSessionID, forward, ports.MessageDeliveryOptions{}); err != nil {
			s.logger.Warn("plan qa note not delivered to orchestrator", "plan", p.ID, "error", err)
		}
	}
	return s.detail(ctx, p)
}

func (s *Service) qaResultLocked(ctx context.Context, id domain.PlanID, in QAResultInput) (domain.Plan, string, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return domain.Plan{}, "", err
	}
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return domain.Plan{}, "", err
	}
	qaIdx := indexOfKind(ms, domain.MilestoneKindQA)
	if p.State != domain.PlanStateActive || qaIdx < 0 || in.SessionID == "" || ms[qaIdx].SessionID != in.SessionID {
		return domain.Plan{}, "", apierr.Forbidden("PLAN_QA_SESSION_REQUIRED", "Only the plan's QA session can record a QA result")
	}
	qa := ms[qaIdx]
	now := s.now().UTC()
	switch {
	case in.Verdict == domain.QAVerdictPass:
		qa.Status = domain.MilestoneStatusDone
		qa.QAVerdict = domain.QAVerdictPass
		qa.QANote = in.Note
		qa.FinishedAt = &now
		qa.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, qa); err != nil {
			return domain.Plan{}, "", err
		}
		s.recordEvent(ctx, p.ID, qa.ID, in.SessionID, EventQAPassed, in.Note)
		return p, "", nil
	case in.MilestoneKey == "":
		qa.Status = domain.MilestoneStatusBlocked
		qa.QAVerdict = domain.QAVerdictFail
		qa.QANote = in.Note
		qa.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, qa); err != nil {
			return domain.Plan{}, "", err
		}
		s.recordEvent(ctx, p.ID, qa.ID, in.SessionID, EventQAFailed, in.Note)
		forward := fmt.Sprintf("<ao-qa-blocked plan=%q>\nQA could not attribute a failure to one milestone and needs triage:\n\n%s\n</ao-qa-blocked>", p.ID, in.Note)
		return p, forward, nil
	}
	idx := indexOfKey(ms, in.MilestoneKey)
	if idx < 0 {
		return domain.Plan{}, "", milestoneNotFound()
	}
	target := ms[idx]
	if target.Kind != domain.MilestoneKindImplement {
		return domain.Plan{}, "", apierr.Invalid("INVALID_QA_RESULT", "A QA failure must name an implement milestone", map[string]any{"field": "milestoneKey"})
	}
	target.QAVerdict = domain.QAVerdictFail
	target.QANote = in.Note
	target.Status = domain.MilestoneStatusReady
	target.FinishedAt = nil
	target.UpdatedAt = now
	if err := s.store.UpdateMilestone(ctx, target); err != nil {
		return domain.Plan{}, "", err
	}
	qa.Status = domain.MilestoneStatusPending
	qa.UpdatedAt = now
	if err := s.store.UpdateMilestone(ctx, qa); err != nil {
		return domain.Plan{}, "", err
	}
	s.recordEvent(ctx, p.ID, target.ID, in.SessionID, EventQAFailed, in.Note)
	return p, "", nil
}

// Events returns up to limit events after the given id, oldest first, the
// cursor for the next page (the last id returned, or after when empty), and
// the keys of the plan's current milestones.
func (s *Service) Events(ctx context.Context, id domain.PlanID, after int64, limit int) (EventsPage, error) {
	if _, err := s.requirePlan(ctx, id); err != nil {
		return EventsPage{}, err
	}
	if limit <= 0 || limit > MaxEventsPage {
		limit = DetailEventLimit
	}
	evs, err := s.store.ListPlanEvents(ctx, id, after, limit)
	if err != nil {
		return EventsPage{}, err
	}
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		return EventsPage{}, err
	}
	page := EventsPage{Events: evs, NextAfter: after, MilestoneKeys: make(map[domain.MilestoneID]string, len(ms))}
	if len(evs) > 0 {
		page.NextAfter = evs[len(evs)-1].ID
	}
	for _, m := range ms {
		page.MilestoneKeys[m.ID] = m.Key
	}
	return page, nil
}

func (s *Service) tick(ctx context.Context) {
	if s.scheduler == nil {
		return
	}
	if err := s.scheduler.Tick(ctx, s.now().UTC()); err != nil {
		s.logger.Warn("plan scheduler tick failed", "error", err)
	}
}

func (s *Service) detail(ctx context.Context, p domain.Plan) (Detail, error) {
	ms, err := s.store.ListMilestones(ctx, p.ID)
	if err != nil {
		return Detail{}, err
	}
	evs, err := s.store.ListRecentPlanEvents(ctx, p.ID, DetailEventLimit)
	if err != nil {
		return Detail{}, err
	}
	views := s.milestoneViews(ctx, ms)
	return Detail{Summary: summarize(p, views), Milestones: views, Events: evs}, nil
}

func (s *Service) milestoneViews(ctx context.Context, ms []domain.Milestone) []MilestoneView {
	views := make([]MilestoneView, 0, len(ms))
	for _, m := range ms {
		v := MilestoneView{Milestone: m}
		if m.SessionID != "" && s.sessions != nil {
			sess, ok, err := s.sessions.Get(ctx, m.SessionID)
			if err != nil {
				s.logger.Debug("plan milestone session unreadable", "session", m.SessionID, "error", err)
			} else if ok {
				v.Session = &sess
			}
		}
		views = append(views, v)
	}
	return views
}

func summarize(p domain.Plan, views []MilestoneView) Summary {
	ms := make([]domain.Milestone, 0, len(views))
	sum := Summary{Plan: p, LiveSessions: []domain.Session{}}
	for _, v := range views {
		m := v.Milestone
		ms = append(ms, m)
		sum.Counts.Total++
		switch m.Status {
		case domain.MilestoneStatusDone:
			sum.Counts.Done++
		case domain.MilestoneStatusRunning:
			sum.Counts.Running++
		case domain.MilestoneStatusFailed:
			sum.Counts.Failed++
		case domain.MilestoneStatusBlocked:
			sum.Counts.Blocked++
		}
		if v.Session != nil && !v.Session.IsTerminated {
			sum.LiveSessions = append(sum.LiveSessions, *v.Session)
		}
	}
	sum.Phase = domain.DerivePlanPhase(p, ms)
	sum.Kanban, sum.DisplayStatus = domain.DerivePlanKanban(sum.Phase, ms)
	return sum
}

func (s *Service) requireProject(ctx context.Context, id domain.ProjectID) (domain.ProjectRecord, error) {
	if strings.TrimSpace(string(id)) == "" {
		return domain.ProjectRecord{}, apierr.Invalid("INVALID_PROJECT_ID", "Project id is required", nil)
	}
	project, ok, err := s.projects.GetProject(ctx, string(id))
	if err != nil {
		return domain.ProjectRecord{}, err
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return domain.ProjectRecord{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	switch project.Kind.WithDefault() {
	case domain.ProjectKindScratch, domain.ProjectKindWorkspace:
		return domain.ProjectRecord{}, apierr.Invalid("PLAN_PROJECT_KIND_UNSUPPORTED",
			"Plans need a single-repository project; Scratch and multi-repo workspaces are not supported", nil)
	}
	return project, nil
}

func (s *Service) requirePlan(ctx context.Context, id domain.PlanID) (domain.Plan, error) {
	if strings.TrimSpace(string(id)) == "" {
		return domain.Plan{}, apierr.Invalid("INVALID_PLAN_ID", "Plan id is required", nil)
	}
	p, ok, err := s.store.GetPlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if !ok {
		return domain.Plan{}, apierr.NotFound("PLAN_NOT_FOUND", "Unknown plan")
	}
	return p, nil
}

func (s *Service) requireEditable(ctx context.Context, id domain.PlanID) (domain.Plan, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if p.State != domain.PlanStatePlanning {
		return domain.Plan{}, notEditable()
	}
	return p, nil
}

// mutatePlan reads, edits, stamps, and writes one plan. Callers hold s.mu.
func (s *Service) mutatePlan(ctx context.Context, id domain.PlanID, edit func(*domain.Plan) error) (domain.Plan, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if err := edit(&p); err != nil {
		return domain.Plan{}, err
	}
	p.UpdatedAt = s.now().UTC()
	if err := s.store.UpdatePlan(ctx, p); err != nil {
		return domain.Plan{}, err
	}
	return p, nil
}

// touchPlan bumps updated_at after a milestone edit so list ordering and
// "updated" labels reflect it.
func (s *Service) touchPlan(ctx context.Context, id domain.PlanID) {
	if _, err := s.mutatePlan(ctx, id, func(*domain.Plan) error { return nil }); err != nil {
		s.logger.Warn("plan touch failed", "plan", id, "error", err)
	}
}

// recordEvent appends to the plan's activity log. The log is informational,
// so a failed write is logged rather than failing the operation it describes.
func (s *Service) recordEvent(ctx context.Context, planID domain.PlanID, msID domain.MilestoneID, sessionID domain.SessionID, kind, note string) {
	if err := s.store.InsertPlanEvent(ctx, domain.PlanEvent{
		PlanID: planID, MilestoneID: msID, SessionID: sessionID, Kind: kind,
		Note: truncateBytes(note, 500), CreatedAt: s.now().UTC(),
	}); err != nil {
		s.logger.Warn("plan event not recorded", "plan", planID, "kind", kind, "error", err)
	}
}

func validateMilestoneInPlan(m domain.Milestone, others []domain.Milestone) error {
	if err := m.Validate(); err != nil {
		var fe *domain.MilestoneFieldError
		if errors.As(err, &fe) {
			return apierr.Invalid("INVALID_MILESTONE", fe.Message, map[string]any{"field": fe.Field})
		}
		return apierr.Invalid("INVALID_MILESTONE", err.Error(), nil)
	}
	for _, o := range others {
		if o.Key == m.Key {
			return apierr.Conflict("MILESTONE_KEY_EXISTS", fmt.Sprintf("Milestone key %s already exists in this plan", m.Key), nil)
		}
		if m.Kind == domain.MilestoneKindQA && o.Kind == domain.MilestoneKindQA {
			return apierr.Conflict("MILESTONE_KEY_EXISTS", "This plan already has a qa milestone", nil)
		}
	}
	for _, dep := range m.DependsOn {
		idx := indexOfKey(others, dep)
		if idx < 0 {
			return apierr.Invalid("INVALID_MILESTONE", fmt.Sprintf("Unknown dependency %s; add it first", dep), map[string]any{"field": "dependsOn"})
		}
		if others[idx].Kind == domain.MilestoneKindQA {
			return apierr.Invalid("INVALID_MILESTONE", "No milestone can depend on the qa milestone", map[string]any{"field": "dependsOn"})
		}
	}
	return nil
}

func validateTitle(title string) error {
	if title == "" || utf8.RuneCountInString(title) > domain.PlanTitleMaxChars {
		return invalidPlan("title", fmt.Sprintf("Title must be 1-%d characters", domain.PlanTitleMaxChars))
	}
	return nil
}

func validateMaxParallel(n int) error {
	if n < domain.MinPlanMaxParallel || n > domain.MaxPlanMaxParallel {
		return invalidPlan("maxParallel", fmt.Sprintf("Max parallel must be %d-%d", domain.MinPlanMaxParallel, domain.MaxPlanMaxParallel))
	}
	return nil
}

func validateSpec(md string) error {
	if len(md) > SpecMarkdownMaxBytes {
		return invalidPlan("specMarkdown", fmt.Sprintf("Spec must be at most %d bytes", SpecMarkdownMaxBytes))
	}
	return nil
}

func invalidPlan(field, message string) error {
	return apierr.Invalid("INVALID_PLAN", message, map[string]any{"field": field})
}

func notEditable() error {
	return apierr.Conflict("PLAN_NOT_EDITABLE", "This plan is no longer planning, so it cannot be edited", nil)
}

func milestoneNotFound() error {
	return apierr.NotFound("MILESTONE_NOT_FOUND", "Unknown milestone")
}

func graphInvalid(err error) error {
	details := map[string]any{"problem": err.Error()}
	var ge *domain.PlanGraphError
	if errors.As(err, &ge) && len(ge.Keys) > 0 {
		details["keys"] = ge.Keys
	}
	return apierr.Invalid("PLAN_GRAPH_INVALID", err.Error(), details)
}

func storeError(err error) error {
	switch {
	case errors.Is(err, domain.ErrMilestoneKeyExists):
		return apierr.Conflict("MILESTONE_KEY_EXISTS", "This milestone key already exists in the plan", nil)
	case errors.Is(err, domain.ErrProjectUnknown):
		return apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	default:
		return err
	}
}

func projectName(p domain.ProjectRecord) string {
	if strings.TrimSpace(p.DisplayName) != "" {
		return p.DisplayName
	}
	return p.ID
}

func indexOfKey(ms []domain.Milestone, key string) int {
	for i, m := range ms {
		if m.Key == key {
			return i
		}
	}
	return -1
}

func indexOfKind(ms []domain.Milestone, kind domain.MilestoneKind) int {
	for i, m := range ms {
		if m.Kind == kind {
			return i
		}
	}
	return -1
}

func nextOrdinal(ms []domain.Milestone) int {
	next := 0
	for _, m := range ms {
		if m.Ordinal >= next {
			next = m.Ordinal + 1
		}
	}
	return next
}

func depIDs(keys []string, ms []domain.Milestone) []domain.MilestoneID {
	ids := make([]domain.MilestoneID, 0, len(keys))
	for _, k := range keys {
		if i := indexOfKey(ms, k); i >= 0 {
			ids = append(ids, ms[i].ID)
		}
	}
	return ids
}

func normalizeKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

func normalizePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSuffix(strings.TrimSpace(p), "/")
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
