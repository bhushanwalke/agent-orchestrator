// Package plan owns task plans: a ticket the project orchestrator plans into
// milestones, which AO then runs as worker sessions in dependency order and
// verifies with a QA worker.
package plan

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

// Sessions is the session side of a plan: the project orchestrator that plans
// it, the workers that implement it, and guarded delivery of AO messages to
// either. It is a thin adapter over the session service.
type Sessions interface {
	// EnsureOrchestrator returns the project's live orchestrator, starting one
	// when none is running (POST /orchestrators semantics).
	EnsureOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, error)
	SpawnWorker(ctx context.Context, cfg ports.SpawnConfig, project domain.ProjectRecord) (domain.SessionRecord, error)
	// Send delivers text through the guarded path: it refuses rather than
	// interrupts a busy session.
	Send(ctx context.Context, id domain.SessionID, text string, opts ports.MessageDeliveryOptions) error
	// Get reads the session read model (derived status included).
	Get(ctx context.Context, id domain.SessionID) (domain.Session, bool, error)
}

// Projects resolves registered projects.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Scheduler runs one scheduling pass. Activation and retries call it so the
// first workers start without waiting for the periodic tick. Nil until the
// scheduler ships.
type Scheduler interface {
	Tick(ctx context.Context, now time.Time) error
}

// Deps configures a Service.
type Deps struct {
	Store     Store
	Sessions  Sessions
	Projects  Projects
	Scheduler Scheduler
	// Now overrides the clock in tests.
	Now func() time.Time
	// NewID overrides id generation in tests; it receives "plan_" or "ms_".
	NewID  func(prefix string) string
	Logger *slog.Logger
	// BaseContext bounds background work (deferred kickoff retries). The
	// daemon passes its lifetime context; nil uses context.Background.
	BaseContext context.Context
	// KickoffRetryInterval and KickoffRetryTimeout pace the background retry
	// of a kickoff the orchestrator refused (typically while it is still
	// starting). Zero uses 5s and 5m; a negative interval disables retries.
	KickoffRetryInterval time.Duration
	KickoffRetryTimeout  time.Duration
}

// CreateInput is a new plan as the person typed it.
type CreateInput struct {
	ProjectID domain.ProjectID
	Title     string
	Brief     string
	TicketRef string
	// MaxParallel nil uses DefaultPlanMaxParallel; any value must be 1-10.
	MaxParallel *int
}

// Patch edits a plan. Nil fields are left alone.
type Patch struct {
	Title        *string
	MaxParallel  *int
	SpecMarkdown *string
}

// MilestoneInput is a new milestone. Kind defaults to implement.
type MilestoneInput struct {
	Key         string
	Kind        domain.MilestoneKind
	Title       string
	Description string
	DependsOn   []string
	SparsePaths []string
}

// MilestonePatch edits a milestone. Nil fields are left alone.
type MilestonePatch struct {
	Key         *string
	Kind        *domain.MilestoneKind
	Title       *string
	Description *string
	DependsOn   *[]string
	SparsePaths *[]string
}

// QAResultInput is the QA worker's verdict.
type QAResultInput struct {
	Verdict      domain.QAVerdict
	MilestoneKey string
	Note         string
	SessionID    domain.SessionID
}

// MilestoneCounts summarises a plan's milestones by status.
type MilestoneCounts struct {
	Total, Done, Running, Failed, Blocked int
}

// Summary is a plan with everything a board card derives from it.
type Summary struct {
	Plan          domain.Plan
	Phase         domain.PlanPhase
	Kanban        contract.KanbanColumn
	DisplayStatus contract.DisplayStatus
	Counts        MilestoneCounts
	// LiveSessions are the non-terminated sessions currently owning a
	// milestone, in milestone order.
	LiveSessions []domain.Session
}

// MilestoneView is a milestone with its current owning session, if any.
type MilestoneView struct {
	Milestone domain.Milestone
	Session   *domain.Session
}

// Detail is a plan with its milestones and recent events (newest first).
type Detail struct {
	Summary
	Milestones []MilestoneView
	Events     []domain.PlanEvent
}

// EventsPage is one page of a plan's events, oldest first.
type EventsPage struct {
	Events    []domain.PlanEvent
	NextAfter int64
	// MilestoneKeys maps the plan's current milestone ids to keys.
	MilestoneKeys map[domain.MilestoneID]string
}
