package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// InsertPlan persists a new task plan.
func (s *Store) InsertPlan(ctx context.Context, p domain.Plan) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.InsertPlan(ctx, gen.InsertPlanParams{
		ID:                    p.ID,
		ProjectID:             p.ProjectID,
		Title:                 p.Title,
		Brief:                 p.Brief,
		TicketRef:             p.TicketRef,
		SpecMarkdown:          p.SpecMarkdown,
		State:                 p.State,
		OrchestratorSessionID: p.OrchestratorSessionID,
		MaxParallel:           int64(p.MaxParallel),
		CreatedAt:             p.CreatedAt,
		UpdatedAt:             p.UpdatedAt,
		ActivatedAt:           timePtrToNullTime(p.ActivatedAt),
		FinishedAt:            timePtrToNullTime(p.FinishedAt),
	})
	if err != nil {
		if isSQLiteForeignKey(err) {
			return domain.ErrProjectUnknown
		}
		return fmt.Errorf("insert plan %s: %w", p.ID, err)
	}
	return nil
}

// GetPlan looks up one plan, reporting whether it exists.
func (s *Store) GetPlan(ctx context.Context, id domain.PlanID) (domain.Plan, bool, error) {
	row, err := s.qr.SelectPlanByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Plan{}, false, nil
	}
	if err != nil {
		return domain.Plan{}, false, fmt.Errorf("select plan %s: %w", id, err)
	}
	return planFromGen(row), true, nil
}

// ListPlans returns one project's plans newest first. An empty project id
// lists every project's plans.
func (s *Store) ListPlans(ctx context.Context, projectID domain.ProjectID) ([]domain.Plan, error) {
	var rows []gen.Plan
	var err error
	if projectID == "" {
		rows, err = s.qr.SelectAllPlans(ctx)
	} else {
		rows, err = s.qr.SelectPlansByProject(ctx, projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("select plans for project %s: %w", projectID, err)
	}
	return plansFromGen(rows), nil
}

// ListActivePlans returns every active plan in creation order, the order the
// scheduler walks them.
func (s *Store) ListActivePlans(ctx context.Context) ([]domain.Plan, error) {
	rows, err := s.qr.SelectActivePlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("select active plans: %w", err)
	}
	return plansFromGen(rows), nil
}

// UpdatePlan replaces a plan's mutable fields.
func (s *Store) UpdatePlan(ctx context.Context, p domain.Plan) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdatePlan(ctx, gen.UpdatePlanParams{
		Title:                 p.Title,
		Brief:                 p.Brief,
		TicketRef:             p.TicketRef,
		SpecMarkdown:          p.SpecMarkdown,
		State:                 p.State,
		OrchestratorSessionID: p.OrchestratorSessionID,
		MaxParallel:           int64(p.MaxParallel),
		UpdatedAt:             p.UpdatedAt,
		ActivatedAt:           timePtrToNullTime(p.ActivatedAt),
		FinishedAt:            timePtrToNullTime(p.FinishedAt),
		ID:                    p.ID,
	})
	if err != nil {
		return fmt.Errorf("update plan %s: %w", p.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update plan %s: %w", p.ID, sql.ErrNoRows)
	}
	return nil
}

// DeletePlan removes a plan; its milestones, edges, and events cascade.
func (s *Store) DeletePlan(ctx context.Context, id domain.PlanID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.DeletePlan(ctx, id); err != nil {
		return fmt.Errorf("delete plan %s: %w", id, err)
	}
	return nil
}

// InsertMilestone persists one milestone. A key already used in the plan
// reports domain.ErrMilestoneKeyExists. Dependencies are written separately
// with SetMilestoneDeps.
func (s *Store) InsertMilestone(ctx context.Context, m domain.Milestone) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.InsertMilestone(ctx, gen.InsertMilestoneParams{
		ID:          m.ID,
		PlanID:      m.PlanID,
		Key:         m.Key,
		Ordinal:     int64(m.Ordinal),
		Kind:        m.Kind,
		Title:       m.Title,
		Description: m.Description,
		Status:      m.Status,
		SessionID:   m.SessionID,
		SparsePaths: marshalSparsePaths(m.SparsePaths),
		QaVerdict:   m.QAVerdict,
		QaNote:      m.QANote,
		Attempt:     int64(m.Attempt),
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
		StartedAt:   timePtrToNullTime(m.StartedAt),
		FinishedAt:  timePtrToNullTime(m.FinishedAt),
		RetryBase:   int64(m.RetryBase),
		Branch:      m.Branch,
	})
	if err != nil {
		if isSQLiteUnique(err) {
			return domain.ErrMilestoneKeyExists
		}
		return fmt.Errorf("insert milestone %s: %w", m.ID, err)
	}
	return nil
}

// UpdateMilestone replaces a milestone's mutable fields (not its edges).
func (s *Store) UpdateMilestone(ctx context.Context, m domain.Milestone) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateMilestone(ctx, gen.UpdateMilestoneParams{
		Key:         m.Key,
		Ordinal:     int64(m.Ordinal),
		Kind:        m.Kind,
		Title:       m.Title,
		Description: m.Description,
		Status:      m.Status,
		SessionID:   m.SessionID,
		SparsePaths: marshalSparsePaths(m.SparsePaths),
		QaVerdict:   m.QAVerdict,
		QaNote:      m.QANote,
		Attempt:     int64(m.Attempt),
		UpdatedAt:   m.UpdatedAt,
		StartedAt:   timePtrToNullTime(m.StartedAt),
		FinishedAt:  timePtrToNullTime(m.FinishedAt),
		RetryBase:   int64(m.RetryBase),
		Branch:      m.Branch,
		ID:          m.ID,
	})
	if err != nil {
		if isSQLiteUnique(err) {
			return domain.ErrMilestoneKeyExists
		}
		return fmt.Errorf("update milestone %s: %w", m.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("update milestone %s: %w", m.ID, sql.ErrNoRows)
	}
	return nil
}

// DeleteMilestone removes one milestone and every edge touching it.
func (s *Store) DeleteMilestone(ctx context.Context, id domain.MilestoneID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.DeleteMilestone(ctx, id); err != nil {
		return fmt.Errorf("delete milestone %s: %w", id, err)
	}
	return nil
}

// ListMilestones returns a plan's milestones in ordinal order with DependsOn
// filled with the dependency keys.
func (s *Store) ListMilestones(ctx context.Context, planID domain.PlanID) ([]domain.Milestone, error) {
	rows, err := s.qr.SelectMilestonesByPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("select milestones for plan %s: %w", planID, err)
	}
	deps, err := s.qr.SelectMilestoneDepKeysByPlan(ctx, planID)
	if err != nil {
		return nil, fmt.Errorf("select milestone deps for plan %s: %w", planID, err)
	}
	byMilestone := make(map[domain.MilestoneID][]string, len(deps))
	for _, d := range deps {
		byMilestone[d.MilestoneID] = append(byMilestone[d.MilestoneID], d.DependsOnKey)
	}
	out := make([]domain.Milestone, 0, len(rows))
	for _, row := range rows {
		m := milestoneFromGen(row)
		if keys := byMilestone[m.ID]; keys != nil {
			m.DependsOn = keys
		}
		out = append(out, m)
	}
	return out, nil
}

// GetMilestoneBySession finds the milestone a session currently owns.
// DependsOn is not filled.
func (s *Store) GetMilestoneBySession(ctx context.Context, sessionID domain.SessionID) (domain.Milestone, bool, error) {
	row, err := s.qr.SelectMilestoneBySession(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Milestone{}, false, nil
	}
	if err != nil {
		return domain.Milestone{}, false, fmt.Errorf("select milestone for session %s: %w", sessionID, err)
	}
	return milestoneFromGen(row), true, nil
}

// SetMilestoneDeps replaces one milestone's dependency edges atomically.
func (s *Store) SetMilestoneDeps(ctx context.Context, id domain.MilestoneID, dependsOn []domain.MilestoneID) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	if err := q.DeleteMilestoneDeps(ctx, id); err != nil {
		return fmt.Errorf("clear deps of milestone %s: %w", id, err)
	}
	for _, dep := range dependsOn {
		if err := q.InsertMilestoneDep(ctx, gen.InsertMilestoneDepParams{MilestoneID: id, DependsOnID: dep}); err != nil {
			return fmt.Errorf("add dep %s -> %s: %w", id, dep, err)
		}
	}
	return tx.Commit()
}

// InsertPlanEvent appends one entry to a plan's activity log.
func (s *Store) InsertPlanEvent(ctx context.Context, e domain.PlanEvent) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.qw.InsertPlanEvent(ctx, gen.InsertPlanEventParams{
		PlanID:      e.PlanID,
		MilestoneID: e.MilestoneID,
		SessionID:   e.SessionID,
		Kind:        e.Kind,
		Note:        e.Note,
		CreatedAt:   e.CreatedAt,
	}); err != nil {
		return fmt.Errorf("insert plan event %s for %s: %w", e.Kind, e.PlanID, err)
	}
	return nil
}

// ListPlanEvents returns up to limit events with id > afterID, oldest first.
func (s *Store) ListPlanEvents(ctx context.Context, planID domain.PlanID, afterID int64, limit int) ([]domain.PlanEvent, error) {
	rows, err := s.qr.SelectPlanEventsAfter(ctx, gen.SelectPlanEventsAfterParams{PlanID: planID, ID: afterID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("select events for plan %s: %w", planID, err)
	}
	return planEventsFromGen(rows), nil
}

// ListRecentPlanEvents returns a plan's latest limit events, newest first.
func (s *Store) ListRecentPlanEvents(ctx context.Context, planID domain.PlanID, limit int) ([]domain.PlanEvent, error) {
	rows, err := s.qr.SelectRecentPlanEvents(ctx, gen.SelectRecentPlanEventsParams{PlanID: planID, Limit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("select recent events for plan %s: %w", planID, err)
	}
	return planEventsFromGen(rows), nil
}

func plansFromGen(rows []gen.Plan) []domain.Plan {
	out := make([]domain.Plan, 0, len(rows))
	for _, row := range rows {
		out = append(out, planFromGen(row))
	}
	return out
}

func planFromGen(row gen.Plan) domain.Plan {
	return domain.Plan{
		ID:                    row.ID,
		ProjectID:             row.ProjectID,
		Title:                 row.Title,
		Brief:                 row.Brief,
		TicketRef:             row.TicketRef,
		SpecMarkdown:          row.SpecMarkdown,
		State:                 row.State,
		OrchestratorSessionID: row.OrchestratorSessionID,
		MaxParallel:           int(row.MaxParallel),
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
		ActivatedAt:           nullTimeToTimePtr(row.ActivatedAt),
		FinishedAt:            nullTimeToTimePtr(row.FinishedAt),
	}
}

func milestoneFromGen(row gen.PlanMilestone) domain.Milestone {
	return domain.Milestone{
		ID:          row.ID,
		PlanID:      row.PlanID,
		Key:         row.Key,
		Ordinal:     int(row.Ordinal),
		Kind:        row.Kind,
		Title:       row.Title,
		Description: row.Description,
		Status:      row.Status,
		SessionID:   row.SessionID,
		SparsePaths: append([]string{}, unmarshalSparsePaths(row.SparsePaths)...),
		DependsOn:   []string{},
		QAVerdict:   row.QaVerdict,
		QANote:      row.QaNote,
		Attempt:     int(row.Attempt),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		StartedAt:   nullTimeToTimePtr(row.StartedAt),
		FinishedAt:  nullTimeToTimePtr(row.FinishedAt),
		RetryBase:   int(row.RetryBase),
		Branch:      row.Branch,
	}
}

func planEventsFromGen(rows []gen.PlanEvent) []domain.PlanEvent {
	out := make([]domain.PlanEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.PlanEvent{
			ID:          row.ID,
			PlanID:      row.PlanID,
			MilestoneID: row.MilestoneID,
			SessionID:   row.SessionID,
			Kind:        row.Kind,
			Note:        row.Note,
			CreatedAt:   row.CreatedAt,
		})
	}
	return out
}

// marshalSparsePaths stores a sparse cone as a JSON array; nil and empty both
// mean a full checkout and store "[]".
func marshalSparsePaths(paths []string) string {
	if len(paths) == 0 {
		return "[]"
	}
	b, err := json.Marshal(paths)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// unmarshalSparsePaths reads a stored cone; a full checkout reads as nil.
func unmarshalSparsePaths(raw string) []string {
	var paths []string
	if raw == "" || json.Unmarshal([]byte(raw), &paths) != nil || len(paths) == 0 {
		return nil
	}
	return paths
}
