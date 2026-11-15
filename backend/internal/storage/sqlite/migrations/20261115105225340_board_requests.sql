-- A manual board move records what the person asked for: a Task on hold, or
-- a session or Task the person asked to pause (attention), or a session's
-- work accepted without a PR. These are facts the board reads, never a column.

-- +goose Up
ALTER TABLE sessions ADD COLUMN board_request TEXT NOT NULL DEFAULT ''
    CHECK (board_request IN ('', 'attention', 'accepted'));
ALTER TABLE sessions ADD COLUMN board_request_at TIMESTAMP;
ALTER TABLE plans ADD COLUMN board_request TEXT NOT NULL DEFAULT ''
    CHECK (board_request IN ('', 'hold', 'attention'));
ALTER TABLE plans ADD COLUMN board_request_at TIMESTAMP;

-- +goose Down
ALTER TABLE plans DROP COLUMN board_request_at;
ALTER TABLE plans DROP COLUMN board_request;
ALTER TABLE sessions DROP COLUMN board_request_at;
ALTER TABLE sessions DROP COLUMN board_request;
