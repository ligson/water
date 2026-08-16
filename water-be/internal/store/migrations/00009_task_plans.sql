-- +goose Up
CREATE TABLE task_plans (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  contract_goal TEXT NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  UNIQUE (task_id, contract_goal, version)
);

CREATE INDEX idx_task_plans_task_goal
  ON task_plans (task_id, contract_goal, version DESC);

CREATE TABLE task_plan_steps (
  id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL,
  position INTEGER NOT NULL,
  title TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  gate_type TEXT NOT NULL,
  acceptance_json TEXT NOT NULL DEFAULT '[]',
  completed_evidence_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (plan_id) REFERENCES task_plans(id) ON DELETE CASCADE,
  FOREIGN KEY (completed_evidence_id) REFERENCES evidence(id) ON DELETE SET NULL,
  UNIQUE (plan_id, position)
);

CREATE INDEX idx_task_plan_steps_plan_status
  ON task_plan_steps (plan_id, status, position);

-- +goose Down
DROP TABLE task_plan_steps;
DROP TABLE task_plans;
