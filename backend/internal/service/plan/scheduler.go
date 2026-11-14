package plan

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

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
