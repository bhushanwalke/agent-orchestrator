-- retry_base is the attempt count when a person last retried a milestone, so
-- each spawn keeps a unique request id while the retry budget restarts.
-- branch keeps the branch a milestone's worker used after its session row is
-- gone, for defect respawns and stacked dependents.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE plan_milestones ADD COLUMN retry_base INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE plan_milestones ADD COLUMN branch TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE plan_milestones DROP COLUMN branch;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE plan_milestones DROP COLUMN retry_base;
-- +goose StatementEnd
