-- +goose Up
ALTER TABLE turns ADD COLUMN attachments_json TEXT NOT NULL DEFAULT '[]';

-- +goose Down
-- SQLite cannot drop columns safely on all supported versions.
