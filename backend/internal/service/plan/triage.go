package plan

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TriageReminderAfter is how long an auto Task waits for its decision after
// the triage request lands before the orchestrator is reminded, once.
const TriageReminderAfter = domain.TriageReminderAfter

// Triage records how an auto Task is run. plan turns it into an ordinary
// planning plan; direct spawns one worker exactly as the Task tab would and
// ends the plan in the direct state. A plan is decided at most once: any
// later call is refused with PLAN_NOT_IN_TRIAGE and spawns nothing.
func (s *Service) Triage(ctx context.Context, id domain.PlanID, decision domain.TriageDecision, reason string, actor domain.TriageActor) (domain.Plan, error) {
	if !decision.Valid() {
		return domain.Plan{}, apierr.Invalid("INVALID_TRIAGE_DECISION", "decision must be plan or direct", map[string]any{"field": "decision"})
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > domain.TriageReasonMaxBytes {
		return domain.Plan{}, apierr.Invalid("INVALID_TRIAGE_REASON", fmt.Sprintf("reason must be at most %d bytes", domain.TriageReasonMaxBytes), map[string]any{"field": "reason"})
	}
	if !actor.Valid() {
		actor = domain.TriageActorHuman
	}

	s.triageMu.Lock()
	defer s.triageMu.Unlock()
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	// A direct decision deferred by a hold is already the decision.
	if p.State != domain.PlanStateTriage || p.Triage != nil {
		return domain.Plan{}, notInTriage(p)
	}
	project, err := s.requireProject(ctx, p.ProjectID)
	if err != nil {
		return domain.Plan{}, err
	}
	if decision == domain.TriageDecisionPlan {
		return s.decidePlan(ctx, p, project, reason, actor)
	}
	if held(p) {
		return s.deferDirect(ctx, p, reason, actor)
	}
	return s.decideDirect(ctx, p, project, reason, actor)
}

// deferDirect records a direct decision on a held Task without starting its
// worker: the plan stays in triage with the decision set, and releasing the
// hold runs the spawn (runDeferredDirect).
func (s *Service) deferDirect(ctx context.Context, p domain.Plan, reason string, actor domain.TriageActor) (domain.Plan, error) {
	now := s.now().UTC()
	s.mu.Lock()
	next, err := s.mutatePlan(ctx, p.ID, func(p *domain.Plan) error {
		if p.State != domain.PlanStateTriage || p.Triage != nil {
			return notInTriage(*p)
		}
		p.Triage = &domain.PlanTriage{Decision: domain.TriageDecisionDirect, Reason: reason, DecidedBy: actor, DecidedAt: now}
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return domain.Plan{}, err
	}
	s.recordEvent(ctx, p.ID, "", "", EventTriageDeferred, triageNote(*next.Triage))
	return next, nil
}

// runDeferredDirect starts the worker of a direct decision a hold deferred.
func (s *Service) runDeferredDirect(ctx context.Context, p domain.Plan) (domain.Plan, error) {
	s.triageMu.Lock()
	defer s.triageMu.Unlock()
	cur, err := s.requirePlan(ctx, p.ID)
	if err != nil {
		return domain.Plan{}, err
	}
	if cur.State != domain.PlanStateTriage || cur.Triage == nil || cur.Triage.Decision != domain.TriageDecisionDirect {
		return cur, nil
	}
	project, err := s.requireProject(ctx, cur.ProjectID)
	if err != nil {
		return domain.Plan{}, err
	}
	return s.decideDirect(ctx, cur, project, cur.Triage.Reason, cur.Triage.DecidedBy)
}

func (s *Service) decidePlan(ctx context.Context, p domain.Plan, project domain.ProjectRecord, reason string, actor domain.TriageActor) (domain.Plan, error) {
	now := s.now().UTC()
	next := p
	next.State = domain.PlanStatePlanning
	next.Triage = &domain.PlanTriage{Decision: domain.TriageDecisionPlan, Reason: reason, DecidedBy: actor, DecidedAt: now}
	next.UpdatedAt = now
	if err := s.recordDecision(ctx, next); err != nil {
		return domain.Plan{}, err
	}
	s.recordEvent(ctx, p.ID, "", "", EventTriageDecided, triageNote(*next.Triage))
	// The orchestrator plans in the same turn as its own decision. A person's
	// decision needs the ordinary plan request; one not yet delivered goes out
	// as a plan request through the kickoff retry.
	if actor == domain.TriageActorHuman {
		sent, err := s.kickoffSent(ctx, p.ID)
		switch {
		case err != nil:
			s.logger.Warn("plan request after triage not sent", "plan", p.ID, "error", err)
		case sent && next.OrchestratorSessionID != "":
			if err := s.sessions.Send(ctx, next.OrchestratorSessionID, KickoffMessage(next, projectName(project)), ports.MessageDeliveryOptions{}); err != nil {
				s.logger.Info("plan request after triage refused", "plan", p.ID, "error", err)
			}
		default:
			s.scheduleKickoffRetry(p.ID)
		}
	}
	s.Wake()
	return next, nil
}

func (s *Service) decideDirect(ctx context.Context, p domain.Plan, project domain.ProjectRecord, reason string, actor domain.TriageActor) (domain.Plan, error) {
	rec, err := s.sessions.SpawnWorker(ctx, directSpawnConfig(p, project), project)
	if err != nil {
		s.recordEvent(ctx, p.ID, "", "", EventDirectSpawnFailed, err.Error())
		if p.OrchestratorSessionID != "" {
			if sendErr := s.sessions.Send(ctx, p.OrchestratorSessionID, planTriageFailedMessage(p, err.Error()), ports.MessageDeliveryOptions{}); sendErr != nil {
				s.logger.Info("plan triage failure notice refused", "plan", p.ID, "error", sendErr)
			}
		}
		return domain.Plan{}, err
	}
	now := s.now().UTC()
	next := p
	next.State = domain.PlanStateDirect
	next.DirectSessionID = rec.ID
	next.Triage = &domain.PlanTriage{Decision: domain.TriageDecisionDirect, Reason: reason, DecidedBy: actor, DecidedAt: now}
	next.FinishedAt = &now
	next.UpdatedAt = now
	if err := s.recordDecision(ctx, next); err != nil {
		// Cancelled while the worker started: the worker stands, the plan
		// stays cancelled, and the log says which session it was.
		s.recordEvent(ctx, p.ID, "", rec.ID, EventDirectSpawned, "the Task left triage while this worker started")
		return domain.Plan{}, err
	}
	s.recordEvent(ctx, p.ID, "", "", EventTriageDecided, triageNote(*next.Triage))
	s.recordEvent(ctx, p.ID, "", rec.ID, EventDirectSpawned, "")
	return next, nil
}

// recordDecision writes a decision only while the stored plan is in triage.
func (s *Service) recordDecision(ctx context.Context, next domain.Plan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	won, err := s.store.DecidePlanTriage(ctx, next)
	if err != nil {
		return err
	}
	if won {
		return nil
	}
	cur, err := s.requirePlan(ctx, next.ID)
	if err != nil {
		return err
	}
	return notInTriage(cur)
}

// directSpawnConfig is the worker a direct decision starts: the Task tab's
// spawn (session.DelegateTask) with the Task title as its explicit name. It
// carries no plan link, parent, or sparse paths, so it reads as any Task-tab
// worker on the board.
func directSpawnConfig(p domain.Plan, project domain.ProjectRecord) ports.SpawnConfig {
	prompt := directBrief(p)
	requestID := "plan-direct:" + string(p.ID)
	return ports.SpawnConfig{
		ClientRequestID:     requestID,
		ClientRequestHash:   requestID,
		ProjectID:           p.ProjectID,
		Kind:                domain.KindWorker,
		Prompt:              prompt,
		DisplayName:         p.Title,
		DisplayNameExplicit: true,
		KeepDisplayName:     true,
		NameFromPrompt:      project.Kind.WithDefault() != domain.ProjectKindScratch,
		Async:               true,
	}
}

// triageReminder reminds the orchestrator, once, of an auto Task still
// waiting for its decision TriageReminderAfter after the request landed. A
// refused delivery is retried on the next tick. No decision is ever made
// automatically.
func (s *Service) triageReminder(ctx context.Context, p domain.Plan) error {
	if p.State != domain.PlanStateTriage || p.TriageRemindedAt != nil || p.OrchestratorSessionID == "" || held(p) {
		return nil
	}
	sentAt, sent, err := s.kickoffSentAt(ctx, p.ID)
	if err != nil || !sent {
		return err
	}
	now := s.now().UTC()
	if now.Sub(sentAt) < TriageReminderAfter {
		return nil
	}
	if err := s.sessions.Send(ctx, p.OrchestratorSessionID, planTriageReminderMessage(p), ports.MessageDeliveryOptions{}); err != nil {
		// Retried every tick while the orchestrator's controller is down.
		s.logger.Debug("plan triage reminder refused", "plan", p.ID, "error", err)
		return nil
	}
	s.mu.Lock()
	_, err = s.mutatePlan(ctx, p.ID, func(p *domain.Plan) error {
		p.TriageRemindedAt = &now
		return nil
	})
	s.mu.Unlock()
	if err != nil {
		return err
	}
	s.recordEvent(ctx, p.ID, "", p.OrchestratorSessionID, EventTriageReminder, "")
	return nil
}

func triageNote(t domain.PlanTriage) string {
	note := fmt.Sprintf("%s, decided by %s", t.Decision, t.DecidedBy)
	if t.Reason != "" {
		note += ": " + t.Reason
	}
	return note
}

func notInTriage(p domain.Plan) error {
	msg := fmt.Sprintf("This Task is %s, not waiting for a triage decision", p.State)
	if p.Triage != nil {
		msg = fmt.Sprintf("This Task was already decided (%s, by %s)", p.Triage.Decision, p.Triage.DecidedBy)
		if p.Triage.Decision == domain.TriageDecisionPlan && p.State == domain.PlanStatePlanning {
			msg += "; continue with the Plans steps"
		}
	}
	return apierr.Conflict("PLAN_NOT_IN_TRIAGE", msg, map[string]any{"state": string(p.State)})
}
