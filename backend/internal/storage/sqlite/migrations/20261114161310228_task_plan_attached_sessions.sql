-- A plan's run view lists the sessions attached to it (milestone workers and
-- research workers spawned with `ao spawn --plan`).

-- +goose Up
-- +goose StatementBegin
CREATE INDEX sessions_plan_idx ON sessions (plan_id) WHERE plan_id <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS sessions_plan_idx;
-- +goose StatementEnd
