package cli

import (
	"net/http"
	"strings"
	"testing"
)

type cliRoute = struct {
	status int
	body   string
}

func runBoardCLI(t *testing.T, routes map[string]cliRoute, args ...string) ([]planRequest, string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	reqs, server := newPlanServer(t, routes)
	writeRunFileFor(t, cfg, server)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
	return *reqs, out, err
}

func TestSessionMovePostsTheLaneAndPrintsTheStage(t *testing.T) {
	reqs, out, err := runBoardCLI(t, map[string]cliRoute{
		"POST /api/v1/sessions/demo-3/board-move": {http.StatusOK, `{"session":{"id":"demo-3","kanbanColumn":"needs_you","kanbanStage":"needs_review","displayStatus":"Paused for you"}}`},
	}, "session", "move", "demo-3", "--to", "needs_you")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Body["to"] != "needs_you" {
		t.Fatalf("requests = %+v", reqs)
	}
	if out != "session demo-3 is in needs_you (in review): Paused for you\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestSessionMovePrintsTheDaemonRefusal(t *testing.T) {
	_, _, err := runBoardCLI(t, map[string]cliRoute{
		"POST /api/v1/sessions/demo-3/board-move": {http.StatusConflict, `{"error":"conflict","code":"BOARD_MOVE_UNSUPPORTED","message":"This project has no pull request provider, so there is nothing to validate.","details":{"reason":"no_pr_provider"}}`},
	}, "session", "move", "demo-3", "--to", "validating")
	if err == nil || !strings.Contains(err.Error(), "no pull request provider") {
		t.Fatalf("err = %v, want the daemon's message", err)
	}
}

func TestSessionMoveRejectsAnUnknownLane(t *testing.T) {
	reqs, _, err := runBoardCLI(t, nil, "session", "move", "demo-3", "--to", "archive")
	if err == nil || !strings.Contains(err.Error(), "--to must be one of") {
		t.Fatalf("err = %v", err)
	}
	if len(reqs) != 0 {
		t.Fatalf("requests = %+v, want none", reqs)
	}
}

func TestPlanMoveUsesThePlanFromTheEnvironment(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "plan_1")
	reqs, out, err := runBoardCLI(t, map[string]cliRoute{
		"POST /api/v1/plans/plan_1/board-move": {http.StatusOK, `{"plan":{"id":"plan_1","kanbanColumn":"queued","kanbanStage":"queued","displayStatus":"On hold"},"milestones":[],"events":[]}`},
	}, "plan", "move", "--to", "queued")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Path != "/api/v1/plans/plan_1/board-move" || reqs[0].Body["to"] != "queued" {
		t.Fatalf("requests = %+v", reqs)
	}
	if out != "plan plan_1 is in queued: On hold\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestAutomationRunNow(t *testing.T) {
	reqs, out, err := runBoardCLI(t, map[string]cliRoute{
		"POST /api/v1/automations/automation-1/run-now": {http.StatusAccepted, `{"id":"automation-run-x","status":"pending","scheduledFor":"2026-11-15T10:00:00Z"}`},
	}, "automation", "run-now", "automation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost {
		t.Fatalf("requests = %+v", reqs)
	}
	if !strings.HasPrefix(out, "queued run automation-run-x of automation automation-1 for ") {
		t.Fatalf("output = %q", out)
	}
}
