-- +goose Up
ALTER TABLE auth_state ADD COLUMN pin_failed_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE auth_state ADD COLUMN pin_locked_until TEXT NOT NULL DEFAULT '';

-- +goose Down
-- SQLite cannot drop columns safely on all supported versions; rebuild the database to remove these fields.
