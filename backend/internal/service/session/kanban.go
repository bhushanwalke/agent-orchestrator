package session

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func deriveKanbanPresentation(
	rec domain.SessionRecord,
	prs []domain.PRFacts,
	runs []domain.CurrentHeadReviewRun,
	interrupted bool,
	board boardFacts,
	now time.Time,
	signalCapable bool,
) contract.KanbanPresentation {
	facts := toContractKanbanSessionFacts(rec, interrupted, signalCapable)
	facts.LatestReport = contract.ReportState(board.latestReport)
	facts.HasPRProvider = board.hasPRProvider
	return contract.DeriveKanbanPresentation(
		facts,
		toContractKanbanPRFacts(prs, runs),
		now,
		noSignalGrace,
	)
}

// toContractKanbanSessionFacts reads rec.OutputType, the persisted column
// lifecycle.Manager.ReconcileSessionOutputType maintains, as the source of
// truth for artifact-output placement rather than re-scanning the artifact
// directory on every Kanban derivation.
func toContractKanbanSessionFacts(rec domain.SessionRecord, interrupted, signalCapable bool) contract.KanbanSessionFacts {
	return contract.KanbanSessionFacts{
		SessionFacts:      toContractSessionFacts(rec, signalCapable),
		Interrupted:       interrupted,
		HasArtifactOutput: rec.OutputType.HasArtifact(),
		AutoReview:        rec.AutoReviewEnabled,
		AutoInjectReview:  rec.AutoInjectReview,
		AutoInjectCI:      rec.AutoInjectCI,
		Commits:           branchCommits(rec.BranchState),
		PlanManaged:       rec.PlanID != "",
		Provisioning:      rec.ProvisionState.IsProvisioning(),
		BoardRequest:      rec.BoardRequest,
	}
}

func branchCommits(state *domain.SessionBranchState) int {
	if state == nil {
		return 0
	}
	return state.Commits
}

// boardFacts are the board-only facts read outside the session row: the
// newest ao report and whether the project can take a pull request.
type boardFacts struct {
	latestReport  domain.ReportState
	hasPRProvider bool
}

// latestReportStates reads each session's newest report state. Like terminal
// titles it is advisory: a failed read leaves the board without report-based
// Needs you placements rather than failing the list.
func (s *Service) latestReportStates(ctx context.Context, ids []domain.SessionID) map[domain.SessionID]domain.ReportState {
	reader, ok := s.store.(interface {
		ListLatestReportStates(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.ReportState, error)
	})
	if !ok {
		return nil
	}
	states, err := reader.ListLatestReportStates(ctx, ids)
	if err != nil && s.logger != nil {
		s.logger.Warn("list latest report states failed", "err", err)
	}
	return states
}

// prProviderLookup answers, per project and memoized for one read, whether
// the project's origin parses as a provider AO can open pull requests on.
func (s *Service) prProviderLookup(ctx context.Context) func(domain.ProjectID) bool {
	known := map[domain.ProjectID]bool{}
	return func(id domain.ProjectID) bool {
		if has, ok := known[id]; ok {
			return has
		}
		has := false
		if s.scm != nil {
			if project, found, err := s.store.GetProject(ctx, string(id)); err == nil && found && project.RepoOriginURL != "" {
				_, has = s.scm.ParseRepository(project.RepoOriginURL)
			}
		}
		known[id] = has
		return has
	}
}

func toContractKanbanPRFacts(prs []domain.PRFacts, runs []domain.CurrentHeadReviewRun) []contract.KanbanPRFacts {
	byPR := make(map[string]contract.KanbanReviewRunFacts, len(runs))
	for _, run := range runs {
		facts := byPR[run.PRURL]
		facts.Present = true
		facts.Running = facts.Running || run.Status == domain.ReviewRunRunning
		facts.ChangesRequested = facts.ChangesRequested || run.Verdict == domain.VerdictChangesRequested
		facts.Outcome = facts.Outcome || run.Verdict.Valid()
		facts.Failed = facts.Failed || run.Status == domain.ReviewRunFailed
		facts.Cancelled = facts.Cancelled || run.Status == domain.ReviewRunCancelled
		facts.RetryExhausted = facts.RetryExhausted ||
			(run.Status == domain.ReviewRunFailed && (run.AutoRetryBlocked || run.FailedAutoRuns >= domain.AutoReviewFailedRetryLimit))
		byPR[run.PRURL] = facts
	}

	out := make([]contract.KanbanPRFacts, len(prs))
	for i, pr := range prs {
		out[i] = contract.KanbanPRFacts{
			URL:          pr.URL,
			Draft:        pr.Draft,
			Merged:       pr.Merged,
			Closed:       pr.Closed,
			CI:           pr.CI,
			Review:       pr.Review,
			Mergeability: pr.Mergeability,
			UpdatedAt:    pr.UpdatedAt,
			ReviewRun:    byPR[pr.URL],
			ExternalReview: contract.KanbanExternalReviewFacts{
				Approved:         pr.ExternalApproved,
				ChangesRequested: pr.ExternalChangesRequested,
				Comments:         pr.ExternalComments,
			},
		}
	}
	return out
}
