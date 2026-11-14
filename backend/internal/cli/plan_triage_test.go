package cli

import (
	"net/http"
	"strings"
	"testing"
)

const triagedDirectJSON = `{"plan":{"id":"plan_1","projectId":"demo","title":"Demo","state":"direct","phase":"direct","intakeMode":"auto","directSessionId":"demo-4",
"triage":{"decision":"direct","reason":"small","decidedBy":"orchestrator","decidedAt":"2026-11-14T09:00:00Z"}}}`

func runTriageCLI(t *testing.T, body string, args ...string) ([]planRequest, string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	reqs, server := newPlanServer(t, map[string]struct {
		status int
		body   string
	}{"POST /api/v1/plans/plan_1/triage": {http.StatusOK, body}})
	writeRunFileFor(t, cfg, server)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
	return *reqs, out, err
}

func TestPlanTriageDirectFromAnOrchestratorSession(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	t.Setenv("AO_SESSION_ID", "demo-orchestrator-1")
	reqs, out, err := runTriageCLI(t, triagedDirectJSON, "plan", "triage", "plan_1", "--direct", "--reason", "small")
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Method != http.MethodPost || reqs[0].Path != "/api/v1/plans/plan_1/triage" {
		t.Fatalf("requests = %+v", reqs)
	}
	if b := reqs[0].Body; b["decision"] != "direct" || b["reason"] != "small" || b["decidedBy"] != "orchestrator" {
		t.Fatalf("body = %+v", b)
	}
	if out != "plan plan_1 decided: direct; phase is direct; worker session demo-4\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestPlanTriagePlanByAHuman(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	t.Setenv("AO_SESSION_ID", "")
	planned := `{"plan":{"id":"plan_1","state":"planning","phase":"planning","intakeMode":"auto","triage":{"decision":"plan","reason":"","decidedBy":"human","decidedAt":"2026-11-14T09:00:00Z"}}}`
	reqs, out, err := runTriageCLI(t, planned, "plan", "triage", "plan_1", "--plan")
	if err != nil {
		t.Fatal(err)
	}
	if b := reqs[0].Body; b["decision"] != "plan" || b["decidedBy"] != "human" {
		t.Fatalf("body = %+v", b)
	}
	if _, ok := reqs[0].Body["reason"]; ok {
		t.Fatalf("an empty reason was sent: %+v", reqs[0].Body)
	}
	if out != "plan plan_1 decided: plan; phase is planning\n" {
		t.Fatalf("output = %q", out)
	}
	// --by overrides the default.
	reqs, _, err = runTriageCLI(t, planned, "plan", "triage", "plan_1", "--plan", "--by", "orchestrator")
	if err != nil || reqs[0].Body["decidedBy"] != "orchestrator" {
		t.Fatalf("--by orchestrator body = %+v err=%v", reqs[0].Body, err)
	}
}

func TestPlanTriageUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"plan", "triage", "plan_1"},
		{"plan", "triage", "plan_1", "--plan", "--direct"},
		{"plan", "triage", "plan_1", "--direct", "--by", "robot"},
		{"plan", "triage", "plan_1", "--plan", "--reason", strings.Repeat("x", 501)},
		{"plan", "triage", "--plan"},
		{"plan", "create", "--project", "demo", "--title", "t", "--brief", "b", "--mode", "direct"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			t.Setenv("AO_PLAN_ID", "")
			t.Setenv("AO_SESSION_ID", "")
			reqs, _, err := runPlanCLI(t, args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d", err, ExitCode(err))
			}
			if len(reqs) != 0 {
				t.Fatalf("requests sent despite the usage error: %+v", reqs)
			}
		})
	}
}

func TestPlanTriageKeepsTheConflict(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	t.Setenv("AO_SESSION_ID", "")
	cfg := setConfigEnv(t)
	_, server := newPlanServer(t, map[string]struct {
		status int
		body   string
	}{"POST /api/v1/plans/plan_1/triage": {http.StatusConflict,
		`{"error":"conflict","code":"PLAN_NOT_IN_TRIAGE","message":"This Task was already decided (direct, by human)","requestId":"r-1"}`}})
	writeRunFileFor(t, cfg, server)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "plan", "triage", "plan_1", "--direct")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "PLAN_NOT_IN_TRIAGE") || !strings.Contains(err.Error(), "already decided") {
		t.Fatalf("err=%v exit=%d", err, ExitCode(err))
	}
}

func TestPlanCreateSendsMode(t *testing.T) {
	t.Setenv("AO_PLAN_ID", "")
	reqs, out, err := runPlanCLI(t, "plan", "create", "--project", "demo", "--title", "T", "--brief", "b", "--mode", "auto")
	if err != nil {
		t.Fatal(err)
	}
	if reqs[0].Body["mode"] != "auto" {
		t.Fatalf("create body = %+v", reqs[0].Body)
	}
	if !strings.Contains(out, "created plan plan_1") {
		t.Fatalf("output = %q", out)
	}
	reqs, _, err = runPlanCLI(t, "plan", "create", "--project", "demo", "--title", "T", "--brief", "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reqs[0].Body["mode"]; ok {
		t.Fatalf("default create sent a mode: %+v", reqs[0].Body)
	}
}

func TestPlanTriageLine(t *testing.T) {
	if got := planTriageLine(planDTO{ID: "plan_1", IntakeMode: "plan"}); got != "" {
		t.Fatalf("plan-mode line = %q", got)
	}
	if got := planTriageLine(planDTO{ID: "plan_1", IntakeMode: "auto"}); !strings.Contains(got, "waiting for the orchestrator") {
		t.Fatalf("undecided line = %q", got)
	}
	got := planTriageLine(planDTO{ID: "plan_1", IntakeMode: "auto", DirectSessionID: "demo-4",
		Triage: &planTriageInfoDTO{Decision: "direct", Reason: "small", DecidedBy: "human"}})
	if got != "triage: direct, decided by human: small\nworker session: demo-4" {
		t.Fatalf("direct line = %q", got)
	}
}
