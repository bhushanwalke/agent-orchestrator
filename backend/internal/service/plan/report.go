package plan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ReportReader lists a project's worker reports; the plan reads PR links from
// their pr_created outputs.
type ReportReader interface {
	ListReportsByProject(ctx context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error)
}

// OnReport advances the milestone a worker report belongs to. It runs inside
// report creation, so it only records state and wakes the scheduler; it never
// spawns. A report from a session that owns no milestone is a no-op.
func (s *Service) OnReport(ctx context.Context, rec domain.ReportRecord) {
	if rec.SessionID == "" || rec.State == "" {
		return
	}
	// The work branch is read before taking the lock: it may run git.
	var work workBranch
	if rec.State == domain.ReportDone {
		work = s.resolveWorkBranch(ctx, rec)
	}
	s.mu.Lock()
	wake, ask := s.onReportLocked(ctx, rec, work)
	s.mu.Unlock()
	if ask != "" {
		if err := s.sessions.Send(ctx, rec.SessionID, ask, ports.MessageDeliveryOptions{}); err != nil {
			s.logger.Info("plan qa verdict request not delivered", "session", rec.SessionID, "error", err)
		}
	}
	if wake {
		s.Wake()
	}
}

// onReportLocked applies one report. It returns whether the scheduler should
// run and an optional message for the reporting session.
func (s *Service) onReportLocked(ctx context.Context, rec domain.ReportRecord, work workBranch) (bool, string) {
	m, ok, err := s.store.GetMilestoneBySession(ctx, rec.SessionID)
	if err != nil {
		s.logger.Warn("plan report lookup failed", "session", rec.SessionID, "error", err)
		return false, ""
	}
	if !ok {
		return false, ""
	}
	p, found, err := s.store.GetPlan(ctx, m.PlanID)
	if err != nil || !found || p.State != domain.PlanStateActive {
		return false, ""
	}
	now := s.now().UTC()
	save := func(kind, note string) bool {
		m.UpdatedAt = now
		if err := s.store.UpdateMilestone(ctx, m); err != nil {
			s.logger.Warn("plan report not applied", "milestone", m.ID, "error", err)
			return false
		}
		s.recordEvent(ctx, p.ID, m.ID, rec.SessionID, kind, note)
		return true
	}
	live := m.Status == domain.MilestoneStatusRunning || m.Status == domain.MilestoneStatusBlocked
	switch rec.State {
	case domain.ReportCheckpoint:
		if m.Status == domain.MilestoneStatusBlocked {
			m.Status = domain.MilestoneStatusRunning
		}
		save(EventMilestoneCheckpoint, rec.Note)
		return false, ""
	case domain.ReportStuck, domain.ReportNeedsInput:
		if m.Status != domain.MilestoneStatusRunning {
			return false, ""
		}
		m.Status = domain.MilestoneStatusBlocked
		note := rec.Note
		if note == "" {
			note = string(rec.State)
		}
		// The scheduler tells the orchestrator.
		return save(EventMilestoneBlocked, note), ""
	case domain.ReportDone:
		if m.Kind == domain.MilestoneKindImplement {
			if !live {
				return false, ""
			}
			m.Status = domain.MilestoneStatusDone
			m.FinishedAt = &now
			if work.branch != "" {
				m.Branch = work.branch
			}
			if !save(EventMilestoneDone, rec.Note) {
				return false, ""
			}
			if work.unresolved {
				s.recordEvent(ctx, p.ID, m.ID, rec.SessionID, EventMilestoneBranchUnresolved,
					fmt.Sprintf("could not read the worktree's branch; using %s", work.branch))
			}
			return true, ""
		}
		return s.qaDoneLocked(ctx, p, &m, live, save)
	}
	return false, ""
}

// qaDoneLocked handles the QA session's done report. A round that recorded
// failures waits for the fixes; a round that recorded nothing failed and the
// session is asked for its verdict.
func (s *Service) qaDoneLocked(ctx context.Context, p domain.Plan, qa *domain.Milestone, live bool, save func(kind, note string) bool) (bool, string) {
	if !live {
		return false, ""
	}
	switch qa.QAVerdict {
	case domain.QAVerdictFail:
		ms, err := s.store.ListMilestones(ctx, p.ID)
		if err != nil {
			return false, ""
		}
		owned := false
		for _, m := range ms {
			if m.Kind == domain.MilestoneKindImplement && m.QAVerdict == domain.QAVerdictFail {
				owned = true
			}
		}
		if !owned {
			// A failure no milestone owns stays blocked for a person.
			return false, ""
		}
		qa.Status = domain.MilestoneStatusPending
		return save(EventMilestoneDone, "QA round finished with failures; it runs again after the fixes"), ""
	case domain.QAVerdictPass:
		return false, ""
	}
	qa.Status = domain.MilestoneStatusFailed
	// The scheduler asks the session for its verdict until the message lands.
	return save(EventMilestoneFailed, "QA finished without a verdict"), ""
}

func verdictRequestMessage(id domain.PlanID) string {
	return fmt.Sprintf("You reported done without recording a QA verdict. Run `ao plan qa-result %s --pass --note \"...\"` or `ao plan qa-result %s --fail --milestone <key> --note \"...\"` for each failing milestone, then `ao report --done` again.", id, id)
}

// EventMilestoneBranchUnresolved records that a done milestone's work branch
// could not be read and the session's recorded branch was used instead.
const EventMilestoneBranchUnresolved = "milestone_branch_unresolved"

// branchProbeTimeout bounds the git call that reads a worktree's branch.
const branchProbeTimeout = 5 * time.Second

type workBranch struct {
	branch     string
	unresolved bool
}

// resolveWorkBranch finds the branch a done milestone's work is on. Workers
// commit on a topic branch rather than the session's initial one, so the
// session row is the last resort: the branch the report names, then the
// worktree's checked-out branch, then the head branch of a PR the report
// created, then the session's recorded branch (flagged unresolved).
func (s *Service) resolveWorkBranch(ctx context.Context, rec domain.ReportRecord) workBranch {
	m, ok, err := s.store.GetMilestoneBySession(ctx, rec.SessionID)
	if err != nil || !ok || m.Kind != domain.MilestoneKindImplement {
		return workBranch{}
	}
	if b := strings.TrimSpace(rec.Branch); b != "" {
		return workBranch{branch: b}
	}
	sess, found, err := s.sessions.Get(ctx, rec.SessionID)
	if err != nil || !found {
		return workBranch{}
	}
	if s.workspaces != nil && sess.Metadata.WorkspacePath != "" {
		probeCtx, cancel := context.WithTimeout(ctx, branchProbeTimeout)
		b, err := s.workspaces.CurrentBranch(probeCtx, sess.Metadata.WorkspacePath)
		cancel()
		if b = strings.TrimSpace(b); err == nil && b != "" && b != "HEAD" {
			return workBranch{branch: b}
		}
		s.logger.Info("plan milestone branch probe failed", "session", rec.SessionID, "error", err)
	}
	for _, out := range rec.Outputs {
		if out.Kind != domain.ReportOutputPRCreated {
			continue
		}
		for _, pr := range sess.PRs {
			if pr.URL == out.Reference && pr.SourceBranch != "" {
				return workBranch{branch: pr.SourceBranch}
			}
		}
	}
	return workBranch{branch: sess.Metadata.Branch, unresolved: true}
}
