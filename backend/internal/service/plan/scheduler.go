package plan

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// EventPlanDoneNotified records that the orchestrator received <ao-plan-done>.
const EventPlanDoneNotified = "plan_done_notified"

// EventQAVerdictRequested records that a QA session that reported done
// without a verdict was asked for one.
const EventQAVerdictRequested = "qa_verdict_requested"

// Wake asks the plan observer for a tick now instead of at its next interval.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// WakeC is the channel Wake signals; the plan observer polls when it fires.
func (s *Service) WakeC() <-chan struct{} { return s.wake }

// Tick runs one scheduling pass: deferred kickoffs of planning and triage
// plans and the triage reminder, then every active plan in creation order.
// Direct and cancelled plans are never ticked. Ticks never overlap.
func (s *Service) Tick(ctx context.Context, _ time.Time) error {
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	all, err := s.store.ListPlans(ctx, "")
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range all {
		if p.State != domain.PlanStatePlanning && p.State != domain.PlanStateTriage {
			continue
		}
		// A held Task waits; releasing it retries the kickoff (releaseHeld).
		if held(p) {
			continue
		}
		if _, err := s.RetryDeferredKickoff(ctx, p.ID); err != nil {
			errs = append(errs, fmt.Errorf("plan %s kickoff: %w", p.ID, err))
		}
		if err := s.triageReminder(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("plan %s triage reminder: %w", p.ID, err))
		}
	}
	active, err := s.store.ListActivePlans(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, p := range active {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.tickPlan(ctx, p.ID); err != nil {
			errs = append(errs, fmt.Errorf("plan %s: %w", p.ID, err))
		}
	}
	return errors.Join(errs...)
}

// sessionFact is what the scheduler knows about a milestone's session. A
// failed read is unknown, never proof the session ended.
type sessionFact struct {
	known  bool
	found  bool
	ended  bool
	branch string
	// failing is why the agent's turns keep failing, or "" when they do not.
	failing string
}

func (f sessionFact) alive() bool { return f.known && f.found && !f.ended }
func (f sessionFact) gone() bool  { return f.known && (!f.found || f.ended) }

type actionKind int

const (
	actionSpawn actionKind = iota
	actionDeliverDefect
	actionAskVerdict
)

type action struct {
	kind      actionKind
	milestone domain.Milestone
}

func (s *Service) tickPlan(ctx context.Context, id domain.PlanID) error {
	p, err := s.requirePlan(ctx, id)
	if err != nil || p.State != domain.PlanStateActive {
		return err
	}
	if p.FinishedAt != nil {
		// A finished plan only owes the orchestrator its done notice.
		if p.OrchestratorSessionID == "" {
			return nil
		}
		if notified, err := s.hasEvent(ctx, id, EventPlanDoneNotified); err != nil || notified {
			return err
		}
	}
	project, ok, err := s.projects.GetProject(ctx, string(p.ProjectID))
	if err != nil {
		return err
	}
	if !ok {
		s.logger.Warn("plan project is gone; skipping", "plan", id, "project", p.ProjectID)
		return nil
	}
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		return err
	}
	facts := s.sessionFacts(ctx, ms)

	s.mu.Lock()
	actions, err := s.planActionsLocked(ctx, id, facts)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, a := range actions {
		switch a.kind {
		case actionDeliverDefect:
			s.deliverDefect(ctx, id, a.milestone)
		case actionAskVerdict:
			s.askVerdict(ctx, id, a.milestone)
		case actionSpawn:
			// Held from the board: ready milestones wait for the release.
			if held(p) {
				continue
			}
			if err := s.spawnMilestone(ctx, id, project, a.milestone, facts); err != nil {
				return err
			}
		}
	}
	s.notifyOrchestrator(ctx, id)
	return s.finishIfDone(ctx, id, facts)
}

// sessionFacts reads every milestone session once per tick.
func (s *Service) sessionFacts(ctx context.Context, ms []domain.Milestone) map[domain.SessionID]sessionFact {
	facts := make(map[domain.SessionID]sessionFact, len(ms))
	for _, m := range ms {
		if m.SessionID == "" {
			continue
		}
		if _, seen := facts[m.SessionID]; seen {
			continue
		}
		sess, found, err := s.sessions.Get(ctx, m.SessionID)
		if err != nil {
			s.logger.Debug("plan session unreadable this tick", "session", m.SessionID, "error", err)
			facts[m.SessionID] = sessionFact{}
			continue
		}
		facts[m.SessionID] = sessionFact{
			known: true,
			found: found,
			ended: found && (sess.IsTerminated || sess.Activity.State == domain.ActivityExited ||
				sess.ProvisionState == domain.SessionProvisionFailed),
			branch:  sess.Metadata.Branch,
			failing: s.failingTurns(ctx, m.SessionID),
		}
	}
	return facts
}

// failingTurns says why a Chat session's agent keeps failing: the provider
// put the thread in system_error, or its last two turns failed. A session
// without a conversation, or one that cannot be read, is not failing.
func (s *Service) failingTurns(ctx context.Context, id domain.SessionID) string {
	if s.conversations == nil {
		return ""
	}
	conv, err := s.conversations.ConversationForSession(ctx, id)
	if err != nil {
		return ""
	}
	turns, err := s.conversations.RecentConversationTurns(ctx, conv.ID, 2)
	if err != nil {
		turns = nil
	}
	// The provider's own words (a usage limit and when it resets, say) tell
	// the orchestrator and the person why.
	why := func() string {
		for _, turn := range turns {
			if turn.State != domain.TurnStateFailed {
				continue
			}
			if msg := strings.TrimSpace(turn.ErrorMessage); msg != "" {
				return truncateRunes(msg, failingTurnMessageMax)
			}
			if turn.FailureCode != "" {
				return string(turn.FailureCode)
			}
		}
		return ""
	}
	if conv.ThreadState != nil && conv.ThreadState.Status == domain.ThreadStatusSystemError {
		if msg := why(); msg != "" {
			return string(domain.ThreadStatusSystemError) + ": " + msg
		}
		return string(domain.ThreadStatusSystemError)
	}
	if len(turns) < 2 {
		return ""
	}
	for _, turn := range turns {
		if turn.State != domain.TurnStateFailed {
			return ""
		}
	}
	return why()
}

// failingTurnMessageMax clips the provider message in a failing-turns block.
const failingTurnMessageMax = 300

func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit-1]) + "…"
}

// planActionsLocked reconciles and promotes milestones and picks this tick's
// spawns and defect deliveries. Callers hold s.mu.
func (s *Service) planActionsLocked(ctx context.Context, id domain.PlanID, facts map[domain.SessionID]sessionFact) ([]action, error) {
	p, err := s.requirePlan(ctx, id)
	if err != nil || p.State != domain.PlanStateActive {
		return nil, err
	}
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	write := func(m *domain.Milestone, kind, note string) error {
		m.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, *m); err != nil {
			return err
		}
		s.recordEvent(ctx, id, m.ID, m.SessionID, kind, note)
		return nil
	}

	// Reconcile: a running or blocked milestone whose session ended without a
	// done report failed. A blocked qa holding a failure no milestone owns
	// waits for a person instead.
	for i := range ms {
		m := &ms[i]
		if (m.Status != domain.MilestoneStatusRunning && m.Status != domain.MilestoneStatusBlocked) || m.SessionID == "" {
			continue
		}
		if m.Kind == domain.MilestoneKindQA && m.Status == domain.MilestoneStatusBlocked && m.QAVerdict == domain.QAVerdictFail {
			continue
		}
		fact := facts[m.SessionID]
		if fact.gone() {
			m.Status = domain.MilestoneStatusFailed
			if err := write(m, EventMilestoneFailed, fmt.Sprintf("session %s ended without reporting done", m.SessionID)); err != nil {
				return nil, err
			}
			continue
		}
		// A live worker whose agent keeps failing waits for a person; its next
		// checkpoint or done report sets it running again.
		if m.Status == domain.MilestoneStatusRunning && fact.failing != "" {
			m.Status = domain.MilestoneStatusBlocked
			if err := write(m, EventMilestoneBlocked, "agent turns are failing: "+fact.failing); err != nil {
				return nil, err
			}
		}
	}
	// Retry: a failed milestone with attempts left goes back to ready once
	// nothing still owns it.
	for i := range ms {
		m := &ms[i]
		if m.Status != domain.MilestoneStatusFailed || m.AttemptsUsed() >= domain.MaxMilestoneAttempts {
			continue
		}
		if m.SessionID != "" && !facts[m.SessionID].gone() {
			continue
		}
		m.Status = domain.MilestoneStatusReady
		if m.Kind == domain.MilestoneKindQA && !allImplementDone(ms) {
			// QA reruns only after the fixes it asked for.
			m.Status = domain.MilestoneStatusPending
		}
		if err := write(m, EventMilestoneReady, fmt.Sprintf("retrying (attempt %d of %d)", m.AttemptsUsed()+1, domain.MaxMilestoneAttempts)); err != nil {
			return nil, err
		}
	}
	// Promote pending milestones whose dependencies are done. The qa milestone
	// depends on every implement milestone.
	byKey := make(map[string]domain.Milestone, len(ms))
	implementTotal, implementDone := 0, 0
	for _, m := range ms {
		byKey[m.Key] = m
		if m.Kind == domain.MilestoneKindImplement {
			implementTotal++
			if m.Status == domain.MilestoneStatusDone {
				implementDone++
			}
		}
	}
	for i := range ms {
		m := &ms[i]
		if m.Status != domain.MilestoneStatusPending {
			continue
		}
		ready := false
		switch m.Kind {
		case domain.MilestoneKindQA:
			ready = implementTotal > 0 && implementDone == implementTotal
		case domain.MilestoneKindImplement:
			ready = true
			for _, dep := range m.DependsOn {
				if byKey[dep].Status != domain.MilestoneStatusDone {
					ready = false
				}
			}
		}
		if ready {
			m.Status = domain.MilestoneStatusReady
			if err := write(m, EventMilestoneReady, ""); err != nil {
				return nil, err
			}
		}
	}

	// Choose actions. A QA defect goes to the milestone's own session while it
	// lives; a new worker starts only when that session is gone.
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].Ordinal < ms[j].Ordinal })
	running := 0
	for _, m := range ms {
		if m.Kind == domain.MilestoneKindImplement && (m.Status == domain.MilestoneStatusRunning || m.Status == domain.MilestoneStatusBlocked) {
			running++
		}
	}
	var actions []action
	for i := range ms {
		m := ms[i]
		if m.Kind == domain.MilestoneKindQA && m.Status == domain.MilestoneStatusFailed && m.QAVerdict == domain.QAVerdictNone &&
			facts[m.SessionID].alive() {
			asked, err := s.verdictRequested(ctx, id, m)
			if err != nil {
				return nil, err
			}
			if !asked {
				actions = append(actions, action{kind: actionAskVerdict, milestone: m})
			}
			continue
		}
		if m.Status != domain.MilestoneStatusReady {
			continue
		}
		if m.Kind == domain.MilestoneKindQA {
			if !allImplementDone(ms) {
				// A retried or re-promoted QA waits for every implement milestone.
				ms[i].Status = domain.MilestoneStatusPending
				if err := write(&ms[i], EventMilestoneReady, "waiting for the implement milestones"); err != nil {
					return nil, err
				}
				continue
			}
			actions = append(actions, action{kind: actionSpawn, milestone: m})
			continue
		}
		if m.QAVerdict == domain.QAVerdictFail && m.SessionID != "" && !facts[m.SessionID].gone() {
			// Alive or unknown: deliver (or wait), never a second worker.
			if facts[m.SessionID].alive() {
				actions = append(actions, action{kind: actionDeliverDefect, milestone: m})
			}
			continue
		}
		if running >= p.MaxParallel {
			continue
		}
		running++
		actions = append(actions, action{kind: actionSpawn, milestone: m})
	}
	return actions, nil
}

// deliverDefect sends a QA defect to the milestone's live session through the
// guarded path. The milestone is marked running first so a done report that
// lands mid-delivery is kept; a refusal puts it back to ready for the next
// tick.
func (s *Service) deliverDefect(ctx context.Context, id domain.PlanID, m domain.Milestone) {
	p, err := s.requirePlan(ctx, id)
	if err != nil || p.State != domain.PlanStateActive {
		return
	}
	s.mu.Lock()
	cur, ok := s.milestoneByID(ctx, id, m.ID)
	if !ok || cur.Status != domain.MilestoneStatusReady || cur.SessionID != m.SessionID {
		s.mu.Unlock()
		return
	}
	cur.Status = domain.MilestoneStatusRunning
	cur.FinishedAt = nil
	cur.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateMilestone(ctx, cur); err != nil {
		s.mu.Unlock()
		s.logger.Warn("plan defect not started", "plan", id, "milestone", m.Key, "error", err)
		return
	}
	s.mu.Unlock()

	sendErr := s.sessions.Send(ctx, m.SessionID, defectMessage(p, m), ports.MessageDeliveryOptions{})
	s.mu.Lock()
	defer s.mu.Unlock()
	if sendErr == nil {
		s.recordEvent(ctx, id, m.ID, m.SessionID, EventDefectSent, "")
		return
	}
	s.logger.Info("plan defect delivery deferred", "plan", id, "milestone", m.Key, "session", m.SessionID, "error", sendErr)
	cur, ok = s.milestoneByID(ctx, id, m.ID)
	if !ok || cur.Status != domain.MilestoneStatusRunning || cur.SessionID != m.SessionID {
		return
	}
	cur.Status = domain.MilestoneStatusReady
	cur.UpdatedAt = s.now().UTC()
	if err := s.store.UpdateMilestone(ctx, cur); err != nil {
		s.logger.Warn("plan defect refusal not recorded", "plan", id, "milestone", m.Key, "error", err)
	}
}

// askVerdict asks a QA session that reported done without a verdict to record
// one, until the message lands.
func (s *Service) askVerdict(ctx context.Context, id domain.PlanID, qa domain.Milestone) {
	if err := s.sessions.Send(ctx, qa.SessionID, verdictRequestMessage(id), ports.MessageDeliveryOptions{}); err != nil {
		s.logger.Info("plan qa verdict request deferred", "plan", id, "session", qa.SessionID, "error", err)
		return
	}
	s.recordEvent(ctx, id, qa.ID, qa.SessionID, EventQAVerdictRequested, "")
}

// verdictRequested reports whether the current QA session was already asked
// for its verdict.
func (s *Service) verdictRequested(ctx context.Context, id domain.PlanID, qa domain.Milestone) (bool, error) {
	asked := false
	var after int64
	for {
		evs, err := s.store.ListPlanEvents(ctx, id, after, MaxEventsPage)
		if err != nil {
			return false, err
		}
		for _, e := range evs {
			if e.MilestoneID != qa.ID {
				continue
			}
			switch e.Kind {
			case EventMilestoneStarted:
				asked = false
			case EventQAVerdictRequested:
				asked = e.SessionID == qa.SessionID
			}
		}
		if len(evs) < MaxEventsPage {
			return asked, nil
		}
		after = evs[len(evs)-1].ID
	}
}

func allImplementDone(ms []domain.Milestone) bool {
	total := 0
	for _, m := range ms {
		if m.Kind != domain.MilestoneKindImplement {
			continue
		}
		total++
		if m.Status != domain.MilestoneStatusDone {
			return false
		}
	}
	return total > 0
}

// spawnMilestone starts one worker for a ready milestone. The plan is re-read
// before and after the spawn so a cancel mid-tick never starts more work and
// still records the session that did start.
func (s *Service) spawnMilestone(ctx context.Context, id domain.PlanID, project domain.ProjectRecord, m domain.Milestone, facts map[domain.SessionID]sessionFact) error {
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		return err
	}
	if p.State != domain.PlanStateActive {
		return nil
	}
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		return err
	}
	attempt := m.Attempt + 1
	cfg, err := s.buildMilestoneSpawn(ctx, p, project, m, ms, facts, attempt)
	if err != nil {
		return err
	}
	rec, spawnErr := s.sessions.SpawnWorker(ctx, cfg, project)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	cur, ok := s.milestoneByID(ctx, id, m.ID)
	if !ok {
		return nil
	}
	cur.Attempt = attempt
	cur.UpdatedAt = now
	if spawnErr != nil {
		if cur.Status == domain.MilestoneStatusReady {
			cur.Status = domain.MilestoneStatusFailed
		}
		if err := s.store.UpdateMilestone(ctx, cur); err != nil {
			return err
		}
		s.recordEvent(ctx, id, cur.ID, "", EventMilestoneFailed, "spawn failed: "+spawnErr.Error())
		return nil
	}
	cur.SessionID = rec.ID
	cur.StartedAt = &now
	cur.FinishedAt = nil
	p, err = s.requirePlan(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case p.State == domain.PlanStateCancelled || cur.Status == domain.MilestoneStatusCancelled:
		cur.Status = domain.MilestoneStatusCancelled
		if err := s.store.UpdateMilestone(ctx, cur); err != nil {
			return err
		}
		s.recordEvent(ctx, id, cur.ID, rec.ID, EventMilestoneStarted, "the plan was cancelled while this worker started")
		return nil
	case cur.Status == domain.MilestoneStatusReady:
		cur.Status = domain.MilestoneStatusRunning
	}
	if cur.Kind == domain.MilestoneKindQA {
		// A new QA round: earlier verdicts are settled.
		cur.QAVerdict, cur.QANote = domain.QAVerdictNone, ""
		if err := s.resetQAVerdictsLocked(ctx, id, now); err != nil {
			return err
		}
	}
	if err := s.store.UpdateMilestone(ctx, cur); err != nil {
		return err
	}
	s.recordEvent(ctx, id, cur.ID, rec.ID, EventMilestoneStarted, fmt.Sprintf("attempt %d", attempt))
	return nil
}

func (s *Service) resetQAVerdictsLocked(ctx context.Context, id domain.PlanID, now time.Time) error {
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.Kind != domain.MilestoneKindImplement || m.QAVerdict == domain.QAVerdictNone {
			continue
		}
		m.QAVerdict = domain.QAVerdictNone
		m.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// buildMilestoneSpawn is the §6 spawn table for one milestone attempt.
func (s *Service) buildMilestoneSpawn(ctx context.Context, p domain.Plan, project domain.ProjectRecord, m domain.Milestone, ms []domain.Milestone, facts map[domain.SessionID]sessionFact, attempt int) (ports.SpawnConfig, error) {
	requestID := fmt.Sprintf("plan:%s:%d", m.ID, attempt)
	cfg := ports.SpawnConfig{
		ProjectID:         p.ProjectID,
		Kind:              domain.KindWorker,
		ParentSessionID:   p.OrchestratorSessionID,
		CreatedBy:         domain.SessionCreatedByOrchestrator,
		DisplayName:       milestoneDisplayName(p, m),
		Async:             true,
		ClientRequestID:   requestID,
		ClientRequestHash: requestID,
	}
	if m.Kind == domain.MilestoneKindQA {
		built, err := s.builtMilestones(ctx, p, ms, facts)
		if err != nil {
			return ports.SpawnConfig{}, err
		}
		cfg.Prompt = qaPrompt(p, built)
		cfg.PlanContext = &ports.PlanSpawnContext{PlanID: p.ID, MilestoneID: m.ID, MilestoneKey: m.Key, Role: RoleQA, PromptSection: planPromptSection(p, m, RoleQA)}
		return cfg, nil
	}
	byKey := make(map[string]domain.Milestone, len(ms))
	for _, other := range ms {
		byKey[other.Key] = other
	}
	deps := make([]depInfo, 0, len(m.DependsOn))
	for _, key := range m.DependsOn {
		dep := byKey[key]
		deps = append(deps, depInfo{Key: dep.Key, Title: dep.Title, Branch: branchOf(dep, facts), SessionID: dep.SessionID})
	}
	cfg.Prompt = implementerPrompt(p, m, deps)
	if m.QAVerdict == domain.QAVerdictFail && m.SessionID != "" {
		// The original worker is gone: continue its branch with the findings.
		cfg.Branch = branchOf(m, facts)
		cfg.Prompt += "\n" + defectMessage(p, m) + "\n"
	} else if len(deps) == 1 && deps[0].Branch != "" {
		// Stack on the single dependency so its PR is the base.
		cfg.BaseBranch = deps[0].Branch
	}
	paths, fellBack := effectiveSparsePaths(project, m)
	cfg.SparsePaths = paths
	if fellBack {
		recorded, err := s.hasEvent(ctx, p.ID, EventSparseDisabled)
		if err != nil {
			return ports.SpawnConfig{}, err
		}
		if !recorded {
			s.recordEvent(ctx, p.ID, m.ID, "", EventSparseDisabled, "the project has sparse checkouts disabled; milestones use full checkouts")
		}
	}
	cfg.PlanContext = &ports.PlanSpawnContext{PlanID: p.ID, MilestoneID: m.ID, MilestoneKey: m.Key, Role: RoleImplementer, PromptSection: planPromptSection(p, m, RoleImplementer)}
	return cfg, nil
}

// builtMilestones describes the implement milestones for QA and the done
// summary: branches, PRs from pr_created report outputs, and done notes.
func (s *Service) builtMilestones(ctx context.Context, p domain.Plan, ms []domain.Milestone, facts map[domain.SessionID]sessionFact) ([]builtInfo, error) {
	notes := map[domain.MilestoneID]string{}
	var after int64
	for {
		evs, err := s.store.ListPlanEvents(ctx, p.ID, after, MaxEventsPage)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if e.Kind == EventMilestoneDone {
				notes[e.MilestoneID] = e.Note
			}
		}
		if len(evs) < MaxEventsPage {
			break
		}
		after = evs[len(evs)-1].ID
	}
	prs := map[domain.SessionID][]string{}
	if s.reports != nil {
		reports, err := s.reports.ListReportsByProject(ctx, p.ProjectID)
		if err != nil {
			s.logger.Warn("plan PR links unavailable", "plan", p.ID, "error", err)
		}
		for _, r := range reports {
			for _, out := range r.Outputs {
				if out.Kind == domain.ReportOutputPRCreated && out.Reference != "" && !contains(prs[r.SessionID], out.Reference) {
					prs[r.SessionID] = append(prs[r.SessionID], out.Reference)
				}
			}
		}
	}
	sorted := append([]domain.Milestone(nil), ms...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Ordinal < sorted[j].Ordinal })
	var out []builtInfo
	for _, m := range sorted {
		if m.Kind != domain.MilestoneKindImplement {
			continue
		}
		out = append(out, builtInfo{
			Key: m.Key, Title: m.Title, Branch: branchOf(m, facts), SessionID: m.SessionID,
			PRs: prs[m.SessionID], Note: notes[m.ID],
		})
	}
	return out, nil
}

// finishIfDone records plan_done once QA passed, then tells the orchestrator
// until the delivery lands.
func (s *Service) finishIfDone(ctx context.Context, id domain.PlanID, facts map[domain.SessionID]sessionFact) error {
	s.mu.Lock()
	p, err := s.requirePlan(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if domain.DerivePlanPhase(p, ms) != domain.PlanPhaseDone {
		s.mu.Unlock()
		return nil
	}
	var qaMilestone domain.Milestone
	for _, m := range ms {
		if m.Kind == domain.MilestoneKindQA {
			qaMilestone = m
		}
	}
	if p.FinishedAt == nil {
		p, err = s.mutatePlan(ctx, id, func(p *domain.Plan) error {
			now := s.now().UTC()
			p.FinishedAt = &now
			return nil
		})
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.recordEvent(ctx, id, "", "", EventPlanDone, "")
	}
	s.mu.Unlock()

	notified, err := s.hasEvent(ctx, id, EventPlanDoneNotified)
	if err != nil || notified || p.OrchestratorSessionID == "" {
		return err
	}
	if orch, found, err := s.sessions.Get(ctx, p.OrchestratorSessionID); err == nil && (!found || orch.IsTerminated) {
		// Nobody to tell; a new orchestrator sees the plan with ao plan ls.
		s.recordEvent(ctx, id, "", p.OrchestratorSessionID, EventPlanDoneNotified, "the orchestrator session has ended; not delivered")
		return nil
	}
	built, err := s.builtMilestones(ctx, p, ms, facts)
	if err != nil {
		return err
	}
	qa := s.qaOutcome(ctx, qaMilestone)
	if err := s.sessions.Send(ctx, p.OrchestratorSessionID, planDoneMessage(p, built, qa), ports.MessageDeliveryOptions{}); err != nil {
		s.logger.Info("plan done notice deferred", "plan", id, "error", err)
		return nil
	}
	s.recordEvent(ctx, id, "", p.OrchestratorSessionID, EventPlanDoneNotified, "")
	return nil
}

func (s *Service) milestoneByID(ctx context.Context, planID domain.PlanID, id domain.MilestoneID) (domain.Milestone, bool) {
	ms, err := s.store.ListMilestones(ctx, planID)
	if err != nil {
		return domain.Milestone{}, false
	}
	for _, m := range ms {
		if m.ID == id {
			return m, true
		}
	}
	return domain.Milestone{}, false
}

func (s *Service) hasEvent(ctx context.Context, id domain.PlanID, kind string) (bool, error) {
	var after int64
	for {
		evs, err := s.store.ListPlanEvents(ctx, id, after, MaxEventsPage)
		if err != nil {
			return false, err
		}
		for _, e := range evs {
			if e.Kind == kind {
				return true, nil
			}
		}
		if len(evs) < MaxEventsPage {
			return false, nil
		}
		after = evs[len(evs)-1].ID
	}
}

// branchOf is the milestone's work branch: the one recorded when it reported
// done (workers commit on a topic branch, not the session's initial one),
// else, for a milestone that never finished, its session's branch.
func branchOf(m domain.Milestone, facts map[domain.SessionID]sessionFact) string {
	if m.Branch != "" {
		return m.Branch
	}
	return facts[m.SessionID].branch
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// effectiveSparsePaths is what a milestone spawn asks for: the milestone's own
// directories when the project allows sparse worktrees (the session manager
// adds the project's alwaysInclude). A project with sparse checkouts disabled
// gets a full checkout instead of a refused spawn, and fellBack tells the
// scheduler to record sparse_disabled once for the plan. The qa milestone
// always gets a full checkout.
func effectiveSparsePaths(project domain.ProjectRecord, m domain.Milestone) (paths []string, fellBack bool) {
	if m.Kind == domain.MilestoneKindQA || len(m.SparsePaths) == 0 {
		return nil, false
	}
	if !project.Config.SparseCheckout.Enabled {
		return nil, true
	}
	return append([]string(nil), m.SparsePaths...), false
}
