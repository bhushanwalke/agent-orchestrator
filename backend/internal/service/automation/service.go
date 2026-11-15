package automation

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	maxDisplayNameRunes = 120
	maxPromptBytes      = 4096
)

// Store is the persistence surface needed for definition management.
type Store interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	CreateAutomation(context.Context, domain.Automation) (domain.Automation, error)
	GetAutomation(context.Context, domain.AutomationID) (domain.Automation, bool, error)
	ListAutomations(context.Context, domain.AutomationFilter) (domain.AutomationPage, error)
	UpdateAutomation(context.Context, domain.Automation) (bool, error)
	DeleteAutomation(context.Context, domain.AutomationID) (bool, error)
	ListAutomationRuns(context.Context, domain.AutomationRunFilter) (domain.AutomationRunPage, error)
	ListLatestAutomationRuns(context.Context, []domain.AutomationID) (map[domain.AutomationID]domain.AutomationRun, error)
	InsertManualAutomationRun(context.Context, domain.AutomationRun) (bool, error)
}

// Deps are the automation service's injectable dependencies.
type Deps struct {
	Store     Store
	Telemetry ports.EventSink
	// Spawner is the existing daemon session service; automations do not add a
	// second launch path.
	Spawner SessionSpawner
	// Plans creates the Task plan of a plan or auto run. Nil fails those runs.
	Plans PlanCreator
	Clock func() time.Time
	NewID func() string
}

// Service owns automation validation and scheduling behavior.
type Service struct {
	store     Store
	telemetry ports.EventSink
	spawner   SessionSpawner
	plans     PlanCreator
	clock     func() time.Time
	newID     func() string
	wake      chan struct{}
}

// New constructs the daemon-owned automation service.
func New(deps Deps) *Service {
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.NewID == nil {
		deps.NewID = uuid.NewString
	}
	return &Service{store: deps.Store, telemetry: deps.Telemetry, spawner: deps.Spawner, plans: deps.Plans, clock: deps.Clock, newID: deps.NewID, wake: make(chan struct{}, 1)}
}

// WakeC fires when the scheduler should tick now instead of at its next
// interval, as after a run-now request.
func (s *Service) WakeC() <-chan struct{} { return s.wake }

func (s *Service) wakeScheduler() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// RunNow queues a run of an enabled automation for now, the board's "Start
// now" for an automation card. The scheduled occurrence still happens: the
// definition's next run time is unchanged. It is refused while the
// automation already has a run pending, spawning, or running.
func (s *Service) RunNow(ctx context.Context, id domain.AutomationID) (domain.AutomationRun, error) {
	definition, err := s.Get(ctx, id)
	if err != nil {
		return domain.AutomationRun{}, err
	}
	if !definition.Enabled {
		return domain.AutomationRun{}, apierr.Conflict("AUTOMATION_DISABLED", "The automation is turned off", nil)
	}
	now := s.clock().UTC()
	run := domain.AutomationRun{
		ID: domain.AutomationRunID("automation-run-" + s.newID()), AutomationID: id,
		ScheduledFor: now, Status: domain.AutomationRunPending, CreatedAt: now, UpdatedAt: now,
	}
	inserted, err := s.store.InsertManualAutomationRun(ctx, run)
	if err != nil {
		return domain.AutomationRun{}, apierr.Internal("AUTOMATION_RUN_FAILED", "Failed to queue the run")
	}
	if !inserted {
		return domain.AutomationRun{}, apierr.Conflict("AUTOMATION_RUN_ACTIVE", "The automation already has a run in progress", nil)
	}
	s.wakeScheduler()
	return run, nil
}

// CreateInput is the validated user intent for one recurring definition.
type CreateInput struct {
	ProjectID   domain.ProjectID
	DisplayName string
	Prompt      string
	TemplateID  string
	UseCase     string
	Kind        domain.SessionKind
	Harness     domain.AgentHarness
	Model       string
	Mode        string
	Effort      string
	// TaskMode is what each run creates; empty is direct.
	TaskMode domain.AutomationTaskMode
	RRule    string
	Cron     string
	Timezone string
	Enabled  *bool
}

// UpdateInput distinguishes omitted fields from explicit false/empty values.
// Supplying either schedule source replaces the schedule atomically.
type UpdateInput struct {
	DisplayName *string
	Prompt      *string
	UseCase     *string
	Kind        *domain.SessionKind
	Harness     *domain.AgentHarness
	Model       *string
	Mode        *string
	Effort      *string
	TaskMode    *domain.AutomationTaskMode
	RRule       *string
	Cron        *string
	Timezone    *string
	Enabled     *bool
}

// Create validates, canonicalizes, and persists an automation definition.
func (s *Service) Create(ctx context.Context, input CreateInput) (domain.Automation, error) {
	if s.store == nil {
		return domain.Automation{}, apierr.Internal("AUTOMATION_CREATE_FAILED", "Failed to create automation")
	}
	if input.TemplateID == "" {
		input.TemplateID = domain.AutomationTemplateCustom
	}
	if input.UseCase == "" {
		input.UseCase = domain.AutomationUseCaseUnknown
	}
	if !domain.ValidAutomationTemplateID(input.TemplateID) {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_TEMPLATE", "Unknown automation template", nil)
	}
	if !domain.ValidAutomationUseCase(input.UseCase) {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_USE_CASE", "Unknown automation use case", nil)
	}
	project, ok, err := s.store.GetProject(ctx, string(input.ProjectID))
	if err != nil {
		return domain.Automation{}, apierr.Internal("AUTOMATION_CREATE_FAILED", "Failed to create automation")
	}
	if !ok || !project.ArchivedAt.IsZero() {
		return domain.Automation{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" || utf8.RuneCountInString(displayName) > maxDisplayNameRunes {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_NAME", "Automation name must be between 1 and 120 characters", nil)
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" || len(prompt) > maxPromptBytes {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_PROMPT", "Automation prompt must be between 1 and 4096 bytes", nil)
	}
	if input.Kind != domain.KindWorker && input.Kind != domain.KindOrchestrator {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_KIND", "Automation kind must be worker or orchestrator", nil)
	}
	if input.Harness != "" && !input.Harness.IsKnown() {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_HARNESS", "Unknown automation harness", nil)
	}
	if input.TaskMode == "" {
		input.TaskMode = domain.AutomationTaskDirect
	}
	if err := validateTaskMode(input.Kind, input.TaskMode); err != nil {
		return domain.Automation{}, err
	}
	now := s.clock().UTC()
	schedule, err := CanonicalizeSchedule(ScheduleInput{
		RRule: input.RRule, Cron: input.Cron, Timezone: input.Timezone,
	}, now)
	if err != nil {
		return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_SCHEDULE", err.Error(), nil)
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	rec := domain.Automation{
		ID:          domain.AutomationID("automation-" + s.newID()),
		ProjectID:   input.ProjectID,
		DisplayName: displayName,
		Prompt:      prompt,
		TemplateID:  input.TemplateID,
		UseCase:     input.UseCase,
		Kind:        input.Kind,
		Harness:     input.Harness,
		Model:       strings.TrimSpace(input.Model),
		Mode:        strings.TrimSpace(input.Mode),
		Effort:      strings.TrimSpace(input.Effort),
		TaskMode:    input.TaskMode,
		RRuleText:   schedule.RRuleText,
		Timezone:    schedule.Timezone,
		Enabled:     enabled,
		NextRunAt:   schedule.NextRunAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	created, err := s.store.CreateAutomation(ctx, rec)
	if err != nil {
		return domain.Automation{}, apierr.Internal("AUTOMATION_CREATE_FAILED", "Failed to create automation")
	}
	if s.telemetry != nil {
		s.telemetry.Emit(context.WithoutCancel(ctx), ports.TelemetryEvent{
			Name: "ao.automation.created", Source: "automation_service", OccurredAt: now,
			Level:   ports.TelemetryLevelInfo,
			Payload: map[string]any{"template_id": created.TemplateID, "use_case": created.UseCase},
		})
	}
	return created, nil
}

// Get returns one automation definition.
func (s *Service) Get(ctx context.Context, id domain.AutomationID) (domain.Automation, error) {
	rec, ok, err := s.store.GetAutomation(ctx, id)
	if err != nil {
		return domain.Automation{}, apierr.Internal("AUTOMATION_READ_FAILED", "Failed to read automation")
	}
	if !ok {
		return domain.Automation{}, apierr.NotFound("AUTOMATION_NOT_FOUND", "Unknown automation")
	}
	latest, err := s.store.ListLatestAutomationRuns(ctx, []domain.AutomationID{id})
	if err != nil {
		return domain.Automation{}, apierr.Internal("AUTOMATION_READ_FAILED", "Failed to read automation")
	}
	if run, found := latest[id]; found {
		rec.LatestRun = &run
	}
	return rec, nil
}

// List returns a normalized page of definitions.
func (s *Service) List(ctx context.Context, filter domain.AutomationFilter) (domain.AutomationPage, error) {
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 0 || filter.Limit > 100 || filter.Offset < 0 {
		return domain.AutomationPage{}, apierr.Invalid("INVALID_PAGINATION", "Limit must be between 1 and 100 and offset cannot be negative", nil)
	}
	page, err := s.store.ListAutomations(ctx, filter)
	if err != nil {
		return domain.AutomationPage{}, apierr.Internal("AUTOMATION_LIST_FAILED", "Failed to list automations")
	}
	ids := make([]domain.AutomationID, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	latest, err := s.store.ListLatestAutomationRuns(ctx, ids)
	if err != nil {
		return domain.AutomationPage{}, apierr.Internal("AUTOMATION_LIST_FAILED", "Failed to list automations")
	}
	for i := range page.Items {
		if run, ok := latest[page.Items[i].ID]; ok {
			latestRun := run
			page.Items[i].LatestRun = &latestRun
		}
	}
	return page, nil
}

// Update validates a partial definition update and recomputes the next future
// occurrence when a schedule changes or a disabled definition is re-enabled.
func (s *Service) Update(ctx context.Context, id domain.AutomationID, input UpdateInput) (domain.Automation, error) {
	rec, err := s.Get(ctx, id)
	if err != nil {
		return domain.Automation{}, err
	}
	if input.UseCase != nil {
		if !domain.ValidAutomationUseCase(*input.UseCase) {
			return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_USE_CASE", "Unknown automation use case", nil)
		}
		rec.UseCase = *input.UseCase
	}
	if input.DisplayName != nil {
		rec.DisplayName = strings.TrimSpace(*input.DisplayName)
		if rec.DisplayName == "" || utf8.RuneCountInString(rec.DisplayName) > maxDisplayNameRunes {
			return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_NAME", "Automation name must be between 1 and 120 characters", nil)
		}
	}
	if input.Prompt != nil {
		rec.Prompt = strings.TrimSpace(*input.Prompt)
		if rec.Prompt == "" || len(rec.Prompt) > maxPromptBytes {
			return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_PROMPT", "Automation prompt must be between 1 and 4096 bytes", nil)
		}
	}
	if input.Kind != nil {
		if *input.Kind != domain.KindWorker && *input.Kind != domain.KindOrchestrator {
			return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_KIND", "Automation kind must be worker or orchestrator", nil)
		}
		rec.Kind = *input.Kind
	}
	if input.Harness != nil {
		if *input.Harness != "" && !input.Harness.IsKnown() {
			return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_HARNESS", "Unknown automation harness", nil)
		}
		if rec.Harness != *input.Harness {
			// Model capabilities belong to the harness; explicit replacements below survive.
			rec.Model, rec.Mode, rec.Effort = "", "", ""
		}
		rec.Harness = *input.Harness
	}
	if input.Model != nil {
		rec.Model = strings.TrimSpace(*input.Model)
	}
	if input.Mode != nil {
		rec.Mode = strings.TrimSpace(*input.Mode)
	}
	if input.Effort != nil {
		rec.Effort = strings.TrimSpace(*input.Effort)
	}
	if input.TaskMode != nil {
		rec.TaskMode = *input.TaskMode
	}
	if rec.TaskMode == "" {
		rec.TaskMode = domain.AutomationTaskDirect
	}
	if err := validateTaskMode(rec.Kind, rec.TaskMode); err != nil {
		return domain.Automation{}, err
	}
	now := s.clock().UTC()
	scheduleChanged := input.RRule != nil || input.Cron != nil || input.Timezone != nil
	if scheduleChanged {
		rruleText, cronText, timezone := "", "", rec.Timezone
		if input.RRule != nil {
			rruleText = *input.RRule
		}
		if input.Cron != nil {
			cronText = *input.Cron
		}
		if input.Timezone != nil {
			timezone = *input.Timezone
		}
		anchorTimezone := ""
		if input.RRule == nil && input.Cron == nil {
			rruleText, anchorTimezone = rec.RRuleText, rec.Timezone
		}
		schedule, scheduleErr := CanonicalizeSchedule(ScheduleInput{RRule: rruleText, Cron: cronText, Timezone: timezone, AnchorTimezone: anchorTimezone}, now)
		if scheduleErr != nil {
			return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_SCHEDULE", scheduleErr.Error(), nil)
		}
		rec.RRuleText, rec.Timezone, rec.NextRunAt = schedule.RRuleText, schedule.Timezone, schedule.NextRunAt
	}
	if input.Enabled != nil {
		wasEnabled := rec.Enabled
		rec.Enabled = *input.Enabled
		if !wasEnabled && rec.Enabled && !scheduleChanged {
			next, nextErr := NextOccurrence(rec.RRuleText, rec.Timezone, now)
			if nextErr != nil {
				return domain.Automation{}, apierr.Invalid("INVALID_AUTOMATION_SCHEDULE", nextErr.Error(), nil)
			}
			rec.NextRunAt = next
		}
	}
	rec.UpdatedAt = now
	ok, err := s.store.UpdateAutomation(ctx, rec)
	if err != nil {
		return domain.Automation{}, apierr.Internal("AUTOMATION_UPDATE_FAILED", "Failed to update automation")
	}
	if !ok {
		return domain.Automation{}, apierr.NotFound("AUTOMATION_NOT_FOUND", "Unknown automation")
	}
	return s.Get(ctx, id)
}

// Delete removes a definition and its run history. Linked sessions survive by
// the database foreign-key contract.
func (s *Service) Delete(ctx context.Context, id domain.AutomationID) error {
	ok, err := s.store.DeleteAutomation(ctx, id)
	if err != nil {
		return apierr.Internal("AUTOMATION_DELETE_FAILED", "Failed to delete automation")
	}
	if !ok {
		return apierr.NotFound("AUTOMATION_NOT_FOUND", "Unknown automation")
	}
	return nil
}

// Runs returns one definition's newest-first durable run history.
func (s *Service) Runs(ctx context.Context, filter domain.AutomationRunFilter) (domain.AutomationRunPage, error) {
	if _, err := s.Get(ctx, filter.AutomationID); err != nil {
		return domain.AutomationRunPage{}, err
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}
	if filter.Limit < 0 || filter.Limit > 100 || filter.Offset < 0 {
		return domain.AutomationRunPage{}, apierr.Invalid("INVALID_PAGINATION", "Limit must be between 1 and 100 and offset cannot be negative", nil)
	}
	page, err := s.store.ListAutomationRuns(ctx, filter)
	if err != nil {
		return domain.AutomationRunPage{}, apierr.Internal("AUTOMATION_RUN_LIST_FAILED", "Failed to list automation runs")
	}
	return page, nil
}

// validateTaskMode accepts a known task mode, and only direct for an
// orchestrator automation.
func validateTaskMode(kind domain.SessionKind, mode domain.AutomationTaskMode) error {
	if !mode.Valid() {
		return apierr.Invalid("INVALID_AUTOMATION_TASK_MODE", "Task mode must be direct, plan, or auto", map[string]any{"field": "taskMode"})
	}
	if kind == domain.KindOrchestrator && mode != domain.AutomationTaskDirect {
		return apierr.Invalid("INVALID_AUTOMATION_TASK_MODE", "An orchestrator automation runs a session, not a Task plan", map[string]any{"field": "taskMode"})
	}
	return nil
}
