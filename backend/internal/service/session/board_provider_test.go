package session

import (
	"context"
	"testing"
	"time"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A local bare origin is not GitHub's owner/repo shorthand, so its sessions
// have no PR provider and a finished worker's card goes to Needs you.
func TestLocalBareOriginHasNoPRProvider(t *testing.T) {
	gh, err := scmgithub.NewProvider(scmgithub.ProviderOptions{SkipTokenPreflight: true})
	if err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]bool{
		"/tmp/ao-e2e-todo3.git":             false,
		"https://github.com/owner/repo.git": true,
	} {
		st := newFakeStore()
		st.sessions["mer-1"] = boardWorker(domain.ActivityIdle, 2)
		st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: origin}
		svc := &Service{store: st, scm: gh, clock: func() time.Time { return boardNow }}
		sess, err := svc.Get(context.Background(), "mer-1")
		if err != nil {
			t.Fatal(err)
		}
		if sess.HasPRProvider != want {
			t.Errorf("origin %q: hasPrProvider = %v, want %v", origin, sess.HasPRProvider, want)
		}
		wantColumn := domain.KanbanNeedsYou
		if want {
			wantColumn = domain.KanbanBuilding
		}
		if sess.KanbanColumn != wantColumn {
			t.Errorf("origin %q: column = %s, want %s", origin, sess.KanbanColumn, wantColumn)
		}
	}
}
