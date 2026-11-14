package plan

import (
	"context"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Orchestrator notices: a milestone that blocks, or fails with no attempts
// left, needs a decision the scheduler cannot make. Each episode (one
// milestone_blocked event, or the milestone_failed that used the last
// attempt) is delivered once through the guarded path, retried every tick
// until it lands; the *_notified event closes the episode.
const (
	EventMilestoneBlockedNotified = "milestone_blocked_notified"
	EventMilestoneFailedNotified  = "milestone_failed_notified"
)

type orchestratorNotice struct {
	milestone domain.Milestone
	// cause is the milestone_blocked or milestone_failed event being reported.
	cause    domain.PlanEvent
	failed   bool
	resolved bool
}

// notifyOrchestrator delivers every owed notice for the plan.
func (s *Service) notifyOrchestrator(ctx context.Context, id domain.PlanID) {
	p, err := s.requirePlan(ctx, id)
	if err != nil || p.State != domain.PlanStateActive || p.OrchestratorSessionID == "" {
		return
	}
	notices, err := s.owedNotices(ctx, id)
	if err != nil || len(notices) == 0 {
		return
	}
	orch := p.OrchestratorSessionID
	if sess, found, err := s.sessions.Get(ctx, orch); err == nil && (!found || sess.IsTerminated) {
		for _, n := range notices {
			s.recordEvent(ctx, id, n.milestone.ID, orch, n.notifiedKind(), "the orchestrator session has ended; not delivered")
		}
		return
	}
	for _, n := range notices {
		if err := s.sessions.Send(ctx, orch, n.message(p), ports.MessageDeliveryOptions{}); err != nil {
			s.logger.Info("plan orchestrator notice deferred", "plan", id, "milestone", n.milestone.Key, "error", err)
			return
		}
		note := ""
		if n.resolved {
			note = "the milestone was running again before delivery"
		}
		s.recordEvent(ctx, id, n.milestone.ID, orch, n.notifiedKind(), note)
	}
}

// owedNotices finds each milestone's latest block and exhausted failure that
// no notice has covered yet.
func (s *Service) owedNotices(ctx context.Context, id domain.PlanID) ([]orchestratorNotice, error) {
	ms, err := s.store.ListMilestones(ctx, id)
	if err != nil {
		return nil, err
	}
	type latest struct {
		blocked, failed                 domain.PlanEvent
		blockedNotified, failedNotified int64
	}
	byMilestone := map[domain.MilestoneID]*latest{}
	var after int64
	for {
		evs, err := s.store.ListPlanEvents(ctx, id, after, MaxEventsPage)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			if e.MilestoneID == "" {
				continue
			}
			l := byMilestone[e.MilestoneID]
			if l == nil {
				l = &latest{}
				byMilestone[e.MilestoneID] = l
			}
			switch e.Kind {
			case EventMilestoneBlocked:
				l.blocked = e
			case EventMilestoneFailed:
				l.failed = e
			case EventMilestoneBlockedNotified:
				l.blockedNotified = e.ID
			case EventMilestoneFailedNotified:
				l.failedNotified = e.ID
			}
		}
		if len(evs) < MaxEventsPage {
			break
		}
		after = evs[len(evs)-1].ID
	}
	var out []orchestratorNotice
	for _, m := range ms {
		l := byMilestone[m.ID]
		if l == nil {
			continue
		}
		if l.blocked.ID > l.blockedNotified {
			out = append(out, orchestratorNotice{milestone: m, cause: l.blocked, resolved: m.Status != domain.MilestoneStatusBlocked})
		}
		exhausted := m.Status == domain.MilestoneStatusFailed && m.AttemptsUsed() >= domain.MaxMilestoneAttempts
		if exhausted && l.failed.ID > l.failedNotified {
			out = append(out, orchestratorNotice{milestone: m, cause: l.failed, failed: true})
		}
	}
	return out, nil
}

func (n orchestratorNotice) notifiedKind() string {
	if n.failed {
		return EventMilestoneFailedNotified
	}
	return EventMilestoneBlockedNotified
}

func (n orchestratorNotice) message(p domain.Plan) string {
	m := n.milestone
	session := n.cause.SessionID
	if session == "" {
		session = m.SessionID
	}
	note := strings.TrimSpace(n.cause.Note)
	if note == "" {
		note = "(no note)"
	}
	if n.failed {
		return fmt.Sprintf(`<ao-milestone-failed plan=%q milestone=%q session=%q>
Milestone %s (%s) failed %d times; AO stopped retrying it.
Last failure: %s

Decide: retry it with `+"`ao plan milestone retry %s %s`"+` (after clarifying the task with `+"`ao send`"+` if needed), or `+"`ao plan cancel %s`"+`, or tell the human.
</ao-milestone-failed>`, string(p.ID), m.Key, string(session), m.Key, m.Title, m.AttemptsUsed(), note, p.ID, m.Key, p.ID)
	}
	resolved := ""
	if n.resolved {
		resolved = fmt.Sprintf("\n\nSince then the worker reported again and the milestone is %s; check whether it still needs you.", strings.ReplaceAll(string(m.Status), "_", " "))
		if m.Status == domain.MilestoneStatusRunning {
			resolved = "\n\nSince then the worker reported again and the milestone is running again; check whether it still needs you."
		}
	}
	return fmt.Sprintf(`<ao-milestone-blocked plan=%q milestone=%q session=%q>
Milestone %s (%s) is blocked: %s%s

Decide: clarify the task with `+"`ao send --session %s --message \"...\"`"+`, or `+"`ao plan cancel %s`"+`, or tell the human.
</ao-milestone-blocked>`, string(p.ID), m.Key, string(session), m.Key, m.Title, note, resolved, session, p.ID)
}
