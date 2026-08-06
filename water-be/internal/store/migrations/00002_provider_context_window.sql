-- +goose Up
ALTER TABLE providers ADD COLUMN context_window_tokens INTEGER NOT NULL DEFAULT 8192;

-- +goose Down
ALTER TABLE providers DROP COLUMN context_window_tokens;
