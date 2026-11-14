package plan

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store is the plan service's persistence surface. The SQLite store satisfies
// it; the interface lives next to its only consumer.
type Store interface {
	InsertPlan(ctx context.Context, p domain.Plan) error
	GetPlan(ctx context.Context, id domain.PlanID) (domain.Plan, bool, error)
	ListPlans(ctx context.Context, projectID domain.ProjectID) ([]domain.Plan, error)
	ListActivePlans(ctx context.Context) ([]domain.Plan, error)
	UpdatePlan(ctx context.Context, p domain.Plan) error
	InsertMilestone(ctx context.Context, m domain.Milestone) error
	UpdateMilestone(ctx context.Context, m domain.Milestone) error
	DeleteMilestone(ctx context.Context, id domain.MilestoneID) error
	ListMilestones(ctx context.Context, planID domain.PlanID) ([]domain.Milestone, error)
	GetMilestoneBySession(ctx context.Context, sessionID domain.SessionID) (domain.Milestone, bool, error)
	SetMilestoneDeps(ctx context.Context, id domain.MilestoneID, dependsOn []domain.MilestoneID) error
	InsertPlanEvent(ctx context.Context, e domain.PlanEvent) error
	ListPlanEvents(ctx context.Context, planID domain.PlanID, afterID int64, limit int) ([]domain.PlanEvent, error)
	ListRecentPlanEvents(ctx context.Context, planID domain.PlanID, limit int) ([]domain.PlanEvent, error)
	ListPlanAttachedSessionIDs(ctx context.Context, planID domain.PlanID) ([]domain.SessionID, error)
}
