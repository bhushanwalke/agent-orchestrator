package daemon

import (
	"context"
	"errors"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	planobserver "github.com/aoagents/agent-orchestrator/backend/internal/observe/plans"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	plansvc "github.com/aoagents/agent-orchestrator/backend/internal/service/plan"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// planSessionService is the slice of the session service plans drive.
type planSessionService interface {
	SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool, requestedMode domain.SessionMode, approval domain.PermissionMode) (domain.Session, error)
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error)
	SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
}

// planSessions adapts the session service to plansvc.Sessions without
// duplicating any of its logic: orchestrator reuse, spawn, and guarded
// delivery all stay in the session service.
type planSessions struct {
	svc planSessionService
}

var _ plansvc.Sessions = planSessions{}

func (p planSessions) EnsureOrchestrator(ctx context.Context, projectID domain.ProjectID) (domain.SessionRecord, error) {
	sess, err := p.svc.SpawnOrchestrator(ctx, projectID, false, "", "")
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return sess.SessionRecord, nil
}

func (p planSessions) SpawnWorker(ctx context.Context, cfg ports.SpawnConfig, _ domain.ProjectRecord) (domain.SessionRecord, error) {
	sess, _, _, err := p.svc.Spawn(ctx, cfg)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	return sess.SessionRecord, nil
}

func (p planSessions) Send(ctx context.Context, id domain.SessionID, text string, opts ports.MessageDeliveryOptions) error {
	return p.svc.SendWithOptions(ctx, id, text, nil, opts)
}

func (p planSessions) Get(ctx context.Context, id domain.SessionID) (domain.Session, bool, error) {
	sess, err := p.svc.Get(ctx, id)
	var ae *apierr.Error
	if errors.As(err, &ae) && ae.Kind == apierr.KindNotFound {
		return domain.Session{}, false, nil
	}
	if err != nil {
		return domain.Session{}, false, err
	}
	return sess, true, nil
}

// startPlans wires the task plan service and starts its scheduler observer.
func startPlans(ctx context.Context, store *sqlite.Store, sessions planSessionService, log *slog.Logger) (*plansvc.Service, <-chan struct{}) {
	svc := plansvc.New(plansvc.Deps{
		Store:       store,
		Sessions:    planSessions{svc: sessions},
		Projects:    store,
		Reports:     store,
		Logger:      log.With("component", "plans"),
		BaseContext: ctx,
	})
	return svc, planobserver.New(svc, planobserver.Config{Logger: log}).Start(ctx)
}
