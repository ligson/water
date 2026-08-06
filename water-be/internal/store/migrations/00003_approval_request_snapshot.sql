-- +goose Up
ALTER TABLE approvals ADD COLUMN request_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE approvals DROP COLUMN request_json;
