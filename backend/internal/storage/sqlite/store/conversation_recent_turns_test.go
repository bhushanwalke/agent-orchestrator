package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRecentConversationTurnsNewestFirstWithoutRolledBack(t *testing.T) {
	ctx := context.Background()
	s, session, conversation := seededChatConversation(t)
	appendBranchPrompt(t, s, session, conversation, "generation-root", "a", "A")
	appendBranchPrompt(t, s, session, conversation, "generation-root", "b", "B")
	appendBranchPrompt(t, s, session, conversation, "generation-root", "c", "C")
	if err := s.SettleTurnByID(ctx, "turn-b", domain.TurnStateFailed, "provider crashed", testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RollbackTurns(ctx, conversation.ID, "turn-c", testNow); err != nil {
		t.Fatal(err)
	}
	turns, err := s.RecentConversationTurns(ctx, conversation.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || turns[0].ID != "turn-b" || turns[1].ID != "turn-a" {
		t.Fatalf("recent turns = %+v", turns)
	}
	if turns[0].State != domain.TurnStateFailed || turns[0].ErrorMessage != "provider crashed" {
		t.Fatalf("newest turn = %+v", turns[0])
	}
}
