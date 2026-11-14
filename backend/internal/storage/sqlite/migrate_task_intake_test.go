package sqlite

import (
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestTaskIntakeMigrationsWidenAndRestorePlanStates(t *testing.T) {
	db := openMigratedTestDB(t)
	at := time.Unix(100, 0).UTC()
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at, config) VALUES ('intake', '/repo/intake', ?, '{}');
INSERT INTO plans (id, project_id, title, brief, state, intake_mode, created_at, updated_at)
VALUES ('plan_t', 'intake', 'T', 'b', 'triage', 'auto', ?, ?);
INSERT INTO plans (id, project_id, title, brief, state, intake_mode, direct_session_id, triage_decision, triage_decided_by, created_at, updated_at)
VALUES ('plan_d', 'intake', 'D', 'b', 'direct', 'auto', 'intake-1', 'direct', 'human', ?, ?);
INSERT INTO automations (id, project_id, display_name, prompt, kind, rrule_text, timezone, next_run_at, created_at, updated_at, task_mode)
VALUES ('auto-1', 'intake', 'A', 'p', 'worker', 'FREQ=DAILY', 'UTC', ?, ?, ?, 'auto');
INSERT INTO automation_runs (id, automation_id, scheduled_for, status, plan_id, created_at, updated_at)
VALUES ('run-1', 'auto-1', ?, 'running', 'plan_t', ?, ?);
`, at, at, at, at, at, at, at, at, at, at); err != nil {
		t.Fatalf("insert rows using the new states and columns: %v", err)
	}
	for _, bad := range []string{
		`UPDATE plans SET state = 'thinking' WHERE id = 'plan_t'`,
		`UPDATE plans SET intake_mode = 'direct' WHERE id = 'plan_t'`,
		`UPDATE plans SET triage_decision = 'maybe' WHERE id = 'plan_t'`,
		`UPDATE plans SET triage_decided_by = 'robot' WHERE id = 'plan_t'`,
		`UPDATE automations SET task_mode = 'session' WHERE id = 'auto-1'`,
	} {
		if _, err := db.Exec(bad); err == nil {
			t.Errorf("%s: accepted, want a CHECK failure", bad)
		}
	}
	if _, err := db.Exec(`DELETE FROM plans WHERE id = 'plan_t'`); err != nil {
		t.Fatal(err)
	}
	var planID *string
	if err := db.QueryRow(`SELECT plan_id FROM automation_runs WHERE id = 'run-1'`).Scan(&planID); err != nil {
		t.Fatal(err)
	}
	if planID != nil {
		t.Fatalf("automation_runs.plan_id = %q after the plan was deleted, want NULL", *planID)
	}

	if err := goose.DownTo(db, "migrations", 20261114161310228); err != nil {
		t.Fatalf("down past the intake migrations: %v", err)
	}
	var state string
	if err := db.QueryRow(`SELECT state FROM plans WHERE id = 'plan_d'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "cancelled" {
		t.Fatalf("direct plan after down = %q, want cancelled", state)
	}
	if _, err := db.Exec(`UPDATE plans SET state = 'triage' WHERE id = 'plan_d'`); err == nil {
		t.Fatal("triage stayed allowed after the down migration")
	}
}
