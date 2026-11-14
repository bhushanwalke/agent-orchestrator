-- A plan milestone worker keeps its milestone key and plan instructions so a
-- restore re-exports AO_MILESTONE_KEY and rebuilds the same system prompt.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN plan_milestone_key TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions ADD COLUMN plan_prompt TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN plan_prompt;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE sessions DROP COLUMN plan_milestone_key;
-- +goose StatementEnd
