-- +goose Up
CREATE TABLE task_contracts (
  task_id TEXT PRIMARY KEY,
  goal TEXT NOT NULL,
  task_type TEXT NOT NULL,
  stage TEXT NOT NULL,
  done_when_json TEXT NOT NULL DEFAULT '[]',
  missing_inputs_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

-- +goose Down
DROP TABLE task_contracts;
