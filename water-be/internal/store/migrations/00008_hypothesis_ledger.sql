-- +goose Up
CREATE TABLE hypotheses (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  contract_goal TEXT NOT NULL,
  claim TEXT NOT NULL,
  status TEXT NOT NULL,
  missing_evidence_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE INDEX idx_hypotheses_task_goal_status
  ON hypotheses (task_id, contract_goal, status, updated_at);

CREATE TABLE evidence (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  turn_id TEXT,
  hypothesis_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  operation TEXT NOT NULL,
  source TEXT NOT NULL,
  resource TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL,
  event_sequence INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (turn_id) REFERENCES turns(id) ON DELETE CASCADE,
  FOREIGN KEY (hypothesis_id) REFERENCES hypotheses(id) ON DELETE CASCADE
);

CREATE INDEX idx_evidence_hypothesis_resource
  ON evidence (hypothesis_id, operation, resource, content_hash, created_at);
CREATE INDEX idx_evidence_task_sequence
  ON evidence (task_id, event_sequence, created_at);

-- +goose Down
DROP TABLE evidence;
DROP TABLE hypotheses;
