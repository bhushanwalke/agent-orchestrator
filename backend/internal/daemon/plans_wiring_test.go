package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakePlanSessionService struct {
	orchClean bool
	orchCalls int
	sentOpts  ports.MessageDeliveryOptions
	sentText  string
	sendErr   error
	getErr    error
	spawnCfg  ports.SpawnConfig
}

func (f *fakePlanSessionService) SpawnOrchestrator(_ context.Context, projectID domain.ProjectID, clean bool, _ domain.SessionMode, _ domain.PermissionMode) (domain.Session, error) {
	f.orchCalls++
	f.orchClean = clean
	return domain.Session{SessionRecord: domain.SessionRecord{ID: "orch-1", ProjectID: projectID}}, nil
}

func (f *fakePlanSessionService) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	f.spawnCfg = cfg
	return domain.Session{SessionRecord: domain.SessionRecord{ID: "w-1"}}, 0, 0, nil
}

func (f *fakePlanSessionService) SendWithOptions(_ context.Context, _ domain.SessionID, message string, _ *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error {
	f.sentText, f.sentOpts = message, options
	return f.sendErr
}

func (f *fakePlanSessionService) Get(context.Context, domain.SessionID) (domain.Session, error) {
	if f.getErr != nil {
		return domain.Session{}, f.getErr
	}
	return domain.Session{SessionRecord: domain.SessionRecord{ID: "w-1"}}, nil
}

func TestPlanSessionsAdapter(t *testing.T) {
	ctx := context.Background()
	fake := &fakePlanSessionService{}
	a := planSessions{svc: fake}

	orch, err := a.EnsureOrchestrator(ctx, "mer")
	if err != nil || orch.ID != "orch-1" || fake.orchClean {
		t.Fatalf("ensure = %+v clean=%v err=%v", orch, fake.orchClean, err)
	}
	if rec, err := a.SpawnWorker(ctx, ports.SpawnConfig{ProjectID: "mer", ClientRequestID: "plan:ms_1:1"}, domain.ProjectRecord{}); err != nil ||
		rec.ID != "w-1" || fake.spawnCfg.ClientRequestID != "plan:ms_1:1" {
		t.Fatalf("spawn = %+v cfg=%+v err=%v", rec, fake.spawnCfg, err)
	}
	if err := a.Send(ctx, "orch-1", "hi", ports.MessageDeliveryOptions{SenderSessionID: "x"}); err != nil ||
		fake.sentText != "hi" || fake.sentOpts.SenderSessionID != "x" {
		t.Fatalf("send text=%q opts=%+v err=%v", fake.sentText, fake.sentOpts, err)
	}
	fake.sendErr = apierr.Conflict("SESSION_COMPOSER_BUSY", "busy", nil)
	if err := a.Send(ctx, "orch-1", "hi", ports.MessageDeliveryOptions{}); err == nil {
		t.Fatal("refusal swallowed")
	}

	if _, ok, err := a.Get(ctx, "w-1"); !ok || err != nil {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	fake.getErr = apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	if _, ok, err := a.Get(ctx, "gone"); ok || err != nil {
		t.Fatalf("missing get ok=%v err=%v", ok, err)
	}
	fake.getErr = errors.New("db down")
	if _, _, err := a.Get(ctx, "w-1"); err == nil {
		t.Fatal("read failure swallowed")
	}
}
