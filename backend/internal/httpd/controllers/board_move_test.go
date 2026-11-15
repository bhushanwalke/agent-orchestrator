package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// boardMovingSessions adds the board-move capability to the fake service.
type boardMovingSessions struct {
	*fakeSessionService
	moves []string
}

func (b *boardMovingSessions) BoardMove(_ context.Context, id domain.SessionID, to domain.KanbanColumn) (domain.Session, error) {
	b.moves = append(b.moves, string(id)+" "+string(to))
	if to == domain.KanbanValidating {
		return domain.Session{}, apierr.Conflict("BOARD_MOVE_UNSUPPORTED", "no provider", map[string]any{"reason": "no_pr_provider"})
	}
	return domain.Session{SessionRecord: domain.SessionRecord{ID: id}, KanbanColumn: domain.KanbanReady, KanbanStage: domain.KanbanReady}, nil
}

func TestSessionBoardMoveRoute(t *testing.T) {
	svc := &boardMovingSessions{fakeSessionService: newFakeSessionService()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/board-move", `{"to":"ready"}`)
	if status != http.StatusOK || !strings.Contains(string(body), `"kanbanColumn":"ready"`) {
		t.Fatalf("move = %d %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/board-move", `{"to":"validating"}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "BOARD_MOVE_UNSUPPORTED") || !strings.Contains(string(body), `"reason":"no_pr_provider"`) {
		t.Fatalf("refused move = %d %s", status, body)
	}
	if _, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/board-move", `{`); status != http.StatusBadRequest {
		t.Fatalf("bad json = %d", status)
	}
	if len(svc.moves) != 2 || svc.moves[0] != "ao-1 ready" {
		t.Fatalf("moves = %v", svc.moves)
	}
}
