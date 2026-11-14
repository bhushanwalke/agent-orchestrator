package sessionmanager

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A restore that had to re-create a sparse session's worktree makes a full
// checkout; the prompt must stop calling it sparse.
func TestSparseSectionFollowsTheActualWorktree(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	full := filepath.Join(t.TempDir(), "full")
	if out, err := exec.Command(git, "init", "-q", full).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sparse := filepath.Join(t.TempDir(), "sparse")
	for _, args := range [][]string{{"init", "-q", sparse}, {"-C", sparse, "config", "core.sparseCheckout", "true"}} {
		if out, err := exec.Command(git, args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	st := newFakeStore()
	st.projects["mer"] = sparseProject("", true)
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: lookPath})
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Prompt: "x", SparsePaths: []string{"src"}})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{full: false, sparse: true, "": true} {
		stored := st.sessions[rec.ID]
		stored.Metadata.WorkspacePath = path
		st.sessions[rec.ID] = stored
		got, err := m.buildSystemPrompt(ctx, rec.Kind, rec.ProjectID, rec.ID, rec.RequestedOutput, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "## Sparse checkout") != want {
			t.Fatalf("workspace %q: sparse section present=%v, want %v", path, !want, want)
		}
	}
}
