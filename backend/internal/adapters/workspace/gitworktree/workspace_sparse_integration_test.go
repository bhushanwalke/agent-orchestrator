package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// setupSparseRepo is an origin clone whose main branch has a/, b/, c/ and a
// root README.
func setupSparseRepo(t *testing.T, git, tmp string) string {
	t.Helper()
	repo := setupOriginClone(t, git, tmp)
	for _, dir := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, dir, "file.txt"), []byte(dir+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, git, repo, "add", ".")
	runGit(t, git, repo, "commit", "-m", "dirs")
	runGit(t, git, repo, "push", "origin", "main")
	return repo
}

func TestWorkspaceIntegrationSparseCreate(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupSparseRepo(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/sparse", SparsePaths: []string{"a", "c"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, info.Path, "sparse-checkout", "list"))); got != "a\nc" {
		t.Fatalf("sparse-checkout list = %q", got)
	}
	for _, present := range []string{"README.md", "a/file.txt", "c/file.txt"} {
		if _, err := os.Stat(filepath.Join(info.Path, present)); err != nil {
			t.Fatalf("%s missing: %v", present, err)
		}
	}
	if _, err := os.Stat(filepath.Join(info.Path, "b")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("b/ present in sparse worktree: %v", err)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, info.Path, "config", "--get", "core.sparseCheckoutCone"))); got != "true" {
		t.Fatalf("core.sparseCheckoutCone = %q", got)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, info.Path, "status", "--porcelain"))); got != "" {
		t.Fatalf("fresh sparse worktree is dirty: %q", got)
	}
	if out, err := runGitAllowFail(git, repo, "config", "--get", "core.sparseCheckout"); err == nil && strings.TrimSpace(out) == "true" {
		t.Fatal("sparse checkout leaked into the human checkout")
	}
	if got := strings.TrimSpace(string(outputGit(t, git, info.Path, "rev-parse", "--abbrev-ref", "HEAD"))); got != "feature/sparse" {
		t.Fatalf("branch = %q", got)
	}
	// Restore of the registered worktree keeps its cone.
	restored, err := ws.Restore(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/sparse"})
	if err != nil || restored.Path != info.Path {
		t.Fatalf("restore = %#v err=%v", restored, err)
	}
	if got := strings.TrimSpace(string(outputGit(t, git, info.Path, "sparse-checkout", "list"))); got != "a\nc" {
		t.Fatalf("cone after restore = %q", got)
	}
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

func TestWorkspaceIntegrationSparseCreateFailureRemovesTheWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupSparseRepo(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/bad", SparsePaths: []string{"a*"}})
	if !errors.Is(err, ports.ErrWorkspaceSparseCheckoutFailed) || !strings.Contains(err.Error(), "directories rather than patterns") {
		t.Fatalf("err = %v, want ErrWorkspaceSparseCheckoutFailed with git stderr", err)
	}
	path := filepath.Join(ws.managedRoot, "proj", "sess")
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("half-made worktree left at %s: %v", path, statErr)
	}
	if list := string(outputGit(t, git, repo, "worktree", "list", "--porcelain")); strings.Contains(list, path) {
		t.Fatalf("worktree still registered:\n%s", list)
	}
	if _, err := runGitAllowFail(git, repo, "rev-parse", "--verify", "refs/heads/feature/bad"); err == nil {
		t.Fatal("branch created for the failed sparse worktree was kept")
	}
}

func TestWorkspaceIntegrationSparseCreateKeepsAnExistingBranch(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupSparseRepo(t, git, tmp)
	runGit(t, git, repo, "branch", "feature/kept", "main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/kept", SparsePaths: []string{"a*"}}); err == nil {
		t.Fatal("bad cone accepted")
	}
	if _, err := runGitAllowFail(git, repo, "rev-parse", "--verify", "refs/heads/feature/kept"); err != nil {
		t.Fatalf("pre-existing branch deleted by the rollback: %v", err)
	}
}

// runGitAllowFail runs git and returns its output and error without failing
// the test, for probes whose failure is the expected answer.
func runGitAllowFail(git, dir string, args ...string) (string, error) {
	out, err := exec.Command(git, append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}
