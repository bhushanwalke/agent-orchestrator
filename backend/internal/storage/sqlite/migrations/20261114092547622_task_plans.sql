-- Task plans: a ticket planned by the orchestrator into milestones, each
-- implemented by a worker and verified by a QA worker. Plan, milestone, and
-- plan-event writes emit a project-level 'plan_updated' change_log row.

-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
-- Rebuilding change_log would invalidate every CDC trigger that writes it, so
-- widen only its event_type CHECK in sqlite_schema (as 0140 did for
-- project_id) and force a schema reload. A guard proves the edit landed.
PRAGMA writable_schema=ON;
UPDATE sqlite_schema
SET sql = replace(sql, '''review_run_updated''', '''review_run_updated'', ''plan_updated''')
WHERE type = 'table' AND name = 'change_log' AND instr(sql, '''plan_updated''') = 0;
PRAGMA writable_schema=RESET;

CREATE TEMP TABLE plan_updated_schema_guard (
    widened INTEGER CHECK (widened = 1)
);
INSERT INTO plan_updated_schema_guard
SELECT COUNT(*) FROM sqlite_schema
WHERE type = 'table' AND name = 'change_log' AND instr(sql, '''plan_updated''') > 0;
DROP TABLE plan_updated_schema_guard;

PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

CREATE TABLE plans (
    id                      TEXT PRIMARY KEY,
    project_id              TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    title                   TEXT NOT NULL,
    brief                   TEXT NOT NULL,
    ticket_ref              TEXT NOT NULL DEFAULT '',
    spec_markdown           TEXT NOT NULL DEFAULT '',
    state                   TEXT NOT NULL CHECK (state IN ('planning', 'active', 'cancelled')),
    orchestrator_session_id TEXT NOT NULL DEFAULT '',
    max_parallel            INTEGER NOT NULL DEFAULT 3 CHECK (max_parallel BETWEEN 1 AND 10),
    created_at              TIMESTAMP NOT NULL,
    updated_at              TIMESTAMP NOT NULL,
    activated_at            TIMESTAMP,
    finished_at             TIMESTAMP
);
CREATE INDEX plans_project_idx ON plans (project_id, created_at);

CREATE TABLE plan_milestones (
    id           TEXT PRIMARY KEY,
    plan_id      TEXT NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    key          TEXT NOT NULL,
    ordinal      INTEGER NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('implement', 'qa')),
    title        TEXT NOT NULL,
    description  TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'running', 'blocked', 'done', 'failed', 'cancelled')),
    session_id   TEXT NOT NULL DEFAULT '',
    sparse_paths TEXT NOT NULL DEFAULT '[]',
    qa_verdict   TEXT NOT NULL DEFAULT '' CHECK (qa_verdict IN ('', 'pass', 'fail')),
    qa_note      TEXT NOT NULL DEFAULT '',
    attempt      INTEGER NOT NULL DEFAULT 0,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    started_at   TIMESTAMP,
    finished_at  TIMESTAMP,
    UNIQUE (plan_id, key)
);
CREATE INDEX plan_milestones_session_idx ON plan_milestones (session_id) WHERE session_id <> '';

CREATE TABLE plan_milestone_deps (
    milestone_id  TEXT NOT NULL REFERENCES plan_milestones (id) ON DELETE CASCADE,
    depends_on_id TEXT NOT NULL REFERENCES plan_milestones (id) ON DELETE CASCADE,
    PRIMARY KEY (milestone_id, depends_on_id)
);

CREATE TABLE plan_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    plan_id      TEXT NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    milestone_id TEXT NOT NULL DEFAULT '',
    session_id   TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL
);
CREATE INDEX plan_events_plan_idx ON plan_events (plan_id, id);

ALTER TABLE sessions ADD COLUMN plan_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN plan_milestone_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN sparse_paths TEXT NOT NULL DEFAULT '[]';

CREATE TRIGGER plans_cdc_insert
AFTER INSERT ON plans
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'plan_updated', json_object('id', NEW.id), NEW.updated_at);
END;

CREATE TRIGGER plans_cdc_update
AFTER UPDATE ON plans
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    VALUES (NEW.project_id, NULL, 'plan_updated', json_object('id', NEW.id), NEW.updated_at);
END;

CREATE TRIGGER plan_milestones_cdc_insert
AFTER INSERT ON plan_milestones
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT p.project_id, NULL, 'plan_updated', json_object('id', p.id), NEW.updated_at
    FROM plans p WHERE p.id = NEW.plan_id;
END;

CREATE TRIGGER plan_milestones_cdc_update
AFTER UPDATE ON plan_milestones
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT p.project_id, NULL, 'plan_updated', json_object('id', p.id), NEW.updated_at
    FROM plans p WHERE p.id = NEW.plan_id;
END;

CREATE TRIGGER plan_events_cdc_insert
AFTER INSERT ON plan_events
BEGIN
    INSERT INTO change_log (project_id, session_id, event_type, payload, created_at)
    SELECT p.project_id, NULL, 'plan_updated', json_object('id', p.id), NEW.created_at
    FROM plans p WHERE p.id = NEW.plan_id;
END;

COMMIT;
PRAGMA foreign_keys=ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
PRAGMA foreign_keys=OFF;
BEGIN IMMEDIATE;

DROP TRIGGER IF EXISTS plan_events_cdc_insert;
DROP TRIGGER IF EXISTS plan_milestones_cdc_update;
DROP TRIGGER IF EXISTS plan_milestones_cdc_insert;
DROP TRIGGER IF EXISTS plans_cdc_update;
DROP TRIGGER IF EXISTS plans_cdc_insert;

ALTER TABLE sessions DROP COLUMN sparse_paths;
ALTER TABLE sessions DROP COLUMN plan_milestone_id;
ALTER TABLE sessions DROP COLUMN plan_id;

DROP TABLE IF EXISTS plan_events;
DROP TABLE IF EXISTS plan_milestone_deps;
DROP TABLE IF EXISTS plan_milestones;
DROP TABLE IF EXISTS plans;

DELETE FROM change_log WHERE event_type = 'plan_updated';

COMMIT;
PRAGMA foreign_keys=ON;

PRAGMA writable_schema=ON;
UPDATE sqlite_schema
SET sql = replace(sql, '''review_run_updated'', ''plan_updated''', '''review_run_updated''')
WHERE type = 'table' AND name = 'change_log';
PRAGMA writable_schema=RESET;
-- +goose StatementEnd
