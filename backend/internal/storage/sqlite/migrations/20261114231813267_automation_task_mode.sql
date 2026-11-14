-- An automation can create a Task plan (or let the orchestrator decide)
-- instead of spawning one session; its runs then point at the plan.

-- +goose Up
ALTER TABLE automations ADD COLUMN task_mode TEXT NOT NULL DEFAULT 'direct'
    CHECK (task_mode IN ('direct', 'plan', 'auto'));
ALTER TABLE automation_runs ADD COLUMN plan_id TEXT REFERENCES plans (id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE automation_runs DROP COLUMN plan_id;
ALTER TABLE automations DROP COLUMN task_mode;
