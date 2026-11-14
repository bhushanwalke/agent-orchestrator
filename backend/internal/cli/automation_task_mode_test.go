package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAutomationCreateAndUpdateSendTaskMode(t *testing.T) {
	cfg := setConfigEnv(t)
	server, capture := automationServer(t, http.StatusCreated, `{"automation":{"id":"automation-1","displayName":"Nightly","taskMode":"auto","nextRunAt":"2026-08-26T03:30:00Z"}}`)
	writeRunFileFor(t, cfg, server)
	deps := Deps{ProcessAlive: func(int) bool { return true }, Now: func() time.Time { return time.Now() }}
	if _, stderr, err := executeCLI(t, deps, "automation", "create", "--project", "demo", "--name", "Nightly", "--prompt", "Tidy", "--cron", "0 2 * * *", "--timezone", "UTC", "--task-mode", "auto"); err != nil {
		t.Fatalf("create: %v stderr=%s", err, stderr)
	}
	var body map[string]any
	if err := json.Unmarshal(capture.body, &body); err != nil || body["taskMode"] != "auto" {
		t.Fatalf("create body=%s err=%v", capture.body, err)
	}
	if _, stderr, err := executeCLI(t, deps, "automation", "update", "automation-1", "--task-mode", "plan"); err != nil {
		t.Fatalf("update: %v stderr=%s", err, stderr)
	}
	body = nil
	if err := json.Unmarshal(capture.body, &body); err != nil || body["taskMode"] != "plan" || len(body) != 1 {
		t.Fatalf("update body=%s err=%v", capture.body, err)
	}
}

func TestAutomationCreateOmitsDefaultTaskModeAndRejectsUnknown(t *testing.T) {
	cfg := setConfigEnv(t)
	server, capture := automationServer(t, http.StatusCreated, `{"automation":{"id":"automation-1","displayName":"Nightly","nextRunAt":"2026-08-26T03:30:00Z"}}`)
	writeRunFileFor(t, cfg, server)
	deps := Deps{ProcessAlive: func(int) bool { return true }, Now: func() time.Time { return time.Now() }}
	if _, _, err := executeCLI(t, deps, "automation", "create", "--project", "demo", "--name", "N", "--prompt", "p", "--cron", "0 2 * * *", "--timezone", "UTC"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(capture.body), "taskMode") {
		t.Fatalf("default create sent a task mode: %s", capture.body)
	}
	for _, args := range [][]string{
		{"automation", "create", "--project", "demo", "--name", "N", "--prompt", "p", "--cron", "0 2 * * *", "--timezone", "UTC", "--task-mode", "session"},
		{"automation", "update", "automation-1", "--task-mode", "session"},
	} {
		if _, _, err := executeCLI(t, deps, args...); err == nil || ExitCode(err) != 2 {
			t.Fatalf("%v: err=%v exit=%d, want a usage error", args, err, ExitCode(err))
		}
	}
}

func TestAutomationRunsPrintPlan(t *testing.T) {
	cfg := setConfigEnv(t)
	server, _ := automationServer(t, http.StatusOK, `{"runs":[
{"id":"r2","automationId":"a","status":"running","planId":"plan_9","scheduledFor":"2026-11-14T02:00:00Z","attemptCount":1},
{"id":"r1","automationId":"a","status":"completed","sessionId":"demo-3","scheduledFor":"2026-11-13T02:00:00Z","attemptCount":1}]}`)
	writeRunFileFor(t, cfg, server)
	out, stderr, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "automation", "runs", "a")
	if err != nil {
		t.Fatalf("runs: %v stderr=%s", err, stderr)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "PLAN") || !strings.Contains(lines[1], "plan_9") || !strings.Contains(lines[2], "demo-3") {
		t.Fatalf("runs output:\n%s", out)
	}
}
