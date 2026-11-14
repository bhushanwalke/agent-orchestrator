-- Task intake modes: an auto Task starts in 'triage' until the orchestrator
-- (or a person) decides between a plan and one direct worker; a direct
-- decision ends the plan in 'direct' with the worker's session id.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
-- Rebuilding plans would drop its CDC triggers and the foreign keys that name
-- it, so widen only its state CHECK in sqlite_schema (as the task_plans
-- migration widened change_log) and force a schema reload. A guard proves the
-- edit landed.
PRAGMA writable_schema=ON;
UPDATE sqlite_schema
SET sql = replace(sql, '''planning'', ''active'', ''cancelled'')', '''planning'', ''active'', ''cancelled'', ''triage'', ''direct'')')
WHERE type = 'table' AND name = 'plans' AND instr(sql, '''triage''') = 0;
PRAGMA writable_schema=RESET;

CREATE TEMP TABLE plan_triage_schema_guard (
    widened INTEGER CHECK (widened = 1)
);
INSERT INTO plan_triage_schema_guard
SELECT COUNT(*) FROM sqlite_schema
WHERE type = 'table' AND name = 'plans' AND instr(sql, '''cancelled'', ''triage'', ''direct''') > 0;
DROP TABLE plan_triage_schema_guard;

ALTER TABLE plans ADD COLUMN intake_mode TEXT NOT NULL DEFAULT 'plan' CHECK (intake_mode IN ('plan', 'auto'));
ALTER TABLE plans ADD COLUMN direct_session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN triage_decision TEXT NOT NULL DEFAULT '' CHECK (triage_decision IN ('', 'plan', 'direct'));
ALTER TABLE plans ADD COLUMN triage_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE plans ADD COLUMN triage_decided_by TEXT NOT NULL DEFAULT '' CHECK (triage_decided_by IN ('', 'orchestrator', 'human'));
ALTER TABLE plans ADD COLUMN triage_decided_at TIMESTAMP;
ALTER TABLE plans ADD COLUMN triage_reminded_at TIMESTAMP;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The narrower CHECK cannot hold the new states, so those plans read as
-- cancelled after a downgrade.
UPDATE plans SET state = 'cancelled' WHERE state IN ('triage', 'direct');

ALTER TABLE plans DROP COLUMN triage_reminded_at;
ALTER TABLE plans DROP COLUMN triage_decided_at;
ALTER TABLE plans DROP COLUMN triage_decided_by;
ALTER TABLE plans DROP COLUMN triage_reason;
ALTER TABLE plans DROP COLUMN triage_decision;
ALTER TABLE plans DROP COLUMN direct_session_id;
ALTER TABLE plans DROP COLUMN intake_mode;

PRAGMA writable_schema=ON;
UPDATE sqlite_schema
SET sql = replace(sql, '''planning'', ''active'', ''cancelled'', ''triage'', ''direct'')', '''planning'', ''active'', ''cancelled'')')
WHERE type = 'table' AND name = 'plans';
PRAGMA writable_schema=RESET;
-- +goose StatementEnd
