package domain

import (
	"errors"
	"time"
)

// PlanID identifies a task plan ("plan_" + uuid).
type PlanID string

// MilestoneID identifies one milestone of a plan ("ms_" + uuid).
type MilestoneID string

// PlanState is the only persisted plan lifecycle fact. Everything a reader
// shows beyond it is derived from the milestones (see DerivePlanPhase).
type PlanState string

// Plan states.
const (
	PlanStatePlanning  PlanState = "planning"
	PlanStateActive    PlanState = "active"
	PlanStateCancelled PlanState = "cancelled"
)

// MilestoneKind distinguishes implementation milestones from the QA milestone.
type MilestoneKind string

// Milestone kinds.
const (
	MilestoneKindImplement MilestoneKind = "implement"
	MilestoneKindQA        MilestoneKind = "qa"
)

// MilestoneStatus is a milestone's scheduling state.
type MilestoneStatus string

// Milestone statuses.
const (
	MilestoneStatusPending   MilestoneStatus = "pending"
	MilestoneStatusReady     MilestoneStatus = "ready"
	MilestoneStatusRunning   MilestoneStatus = "running"
	MilestoneStatusBlocked   MilestoneStatus = "blocked"
	MilestoneStatusDone      MilestoneStatus = "done"
	MilestoneStatusFailed    MilestoneStatus = "failed"
	MilestoneStatusCancelled MilestoneStatus = "cancelled"
)

// QAVerdict is the QA worker's verdict recorded on a milestone.
type QAVerdict string

// QA verdicts. The empty verdict means none was recorded.
const (
	QAVerdictNone QAVerdict = ""
	QAVerdictPass QAVerdict = "pass"
	QAVerdictFail QAVerdict = "fail"
)

// PlanPhase is the derived, never persisted, phase of a plan.
type PlanPhase string

// Plan phases.
const (
	PlanPhasePlanning     PlanPhase = "planning"
	PlanPhaseImplementing PlanPhase = "implementing"
	PlanPhaseQA           PlanPhase = "qa"
	PlanPhaseFixing       PlanPhase = "fixing"
	PlanPhaseDone         PlanPhase = "done"
	PlanPhaseCancelled    PlanPhase = "cancelled"
	PlanPhaseFailed       PlanPhase = "failed"
)

// Plan limits.
const (
	MaxPlanMilestones            = 20
	DefaultPlanMaxParallel       = 3
	MinPlanMaxParallel           = 1
	MaxPlanMaxParallel           = 10
	PlanTitleMaxChars            = 100
	PlanBriefMaxBytes            = 102400
	MilestoneDescriptionMaxBytes = 32768
	QANoteMaxBytes               = 8192
	MaxMilestoneSparsePaths      = 50
	// MaxMilestoneAttempts bounds automatic spawns of one milestone before
	// the plan needs a person.
	MaxMilestoneAttempts = 3
	// QAMilestoneKey is reserved for the plan's single QA milestone.
	QAMilestoneKey = "qa"
)

// Plan is one task: a ticket the orchestrator plans into milestones.
type Plan struct {
	ID                    PlanID
	ProjectID             ProjectID
	Title                 string
	Brief                 string
	TicketRef             string
	SpecMarkdown          string
	State                 PlanState
	OrchestratorSessionID SessionID
	MaxParallel           int
	CreatedAt             time.Time
	UpdatedAt             time.Time
	ActivatedAt           *time.Time
	FinishedAt            *time.Time
}

// Milestone is one unit of a plan, owned by at most one session at a time.
type Milestone struct {
	ID          MilestoneID
	PlanID      PlanID
	Key         string
	Ordinal     int
	Kind        MilestoneKind
	Title       string
	Description string
	Status      MilestoneStatus
	SessionID   SessionID
	SparsePaths []string
	// DependsOn lists the keys of milestones in the same plan this one needs.
	DependsOn  []string
	QAVerdict  QAVerdict
	QANote     string
	Attempt    int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// PlanEvent is one entry in a plan's activity log.
type PlanEvent struct {
	ID          int64
	PlanID      PlanID
	MilestoneID MilestoneID
	SessionID   SessionID
	Kind        string
	Note        string
	CreatedAt   time.Time
}

// ErrMilestoneKeyExists reports a milestone key already used in its plan.
var ErrMilestoneKeyExists = errors.New("milestone key already exists in this plan")
