-- +goose Up
CREATE TABLE scheduled_tasks (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  name TEXT NOT NULL,
  prompt TEXT NOT NULL,
  schedule_type TEXT NOT NULL,
  schedule_expression TEXT NOT NULL,
  timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
  enabled INTEGER NOT NULL DEFAULT 1,
  concurrency_policy TEXT NOT NULL DEFAULT 'skip',
  approval_policy TEXT NOT NULL DEFAULT 'pause',
  max_retries INTEGER NOT NULL DEFAULT 0,
  retry_interval_seconds INTEGER NOT NULL DEFAULT 300,
  next_run_at TEXT,
  last_run_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE INDEX idx_scheduled_tasks_due
  ON scheduled_tasks (enabled, next_run_at);

CREATE TABLE scheduled_task_runs (
  id TEXT PRIMARY KEY,
  scheduled_task_id TEXT NOT NULL,
  task_id TEXT,
  turn_id TEXT,
  trigger_type TEXT NOT NULL,
  status TEXT NOT NULL,
  scheduled_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  attempt INTEGER NOT NULL DEFAULT 1,
  prompt_snapshot TEXT NOT NULL,
  result_summary TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (scheduled_task_id) REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE SET NULL,
  FOREIGN KEY (turn_id) REFERENCES turns(id) ON DELETE SET NULL
);

CREATE INDEX idx_scheduled_task_runs_schedule
  ON scheduled_task_runs (scheduled_task_id, created_at DESC);

CREATE INDEX idx_scheduled_task_runs_queue
  ON scheduled_task_runs (status, scheduled_at);

CREATE UNIQUE INDEX idx_scheduled_task_runs_active
  ON scheduled_task_runs (scheduled_task_id)
  WHERE status IN ('queued', 'running', 'waiting_approval');

-- +goose Down
DROP TABLE scheduled_task_runs;
DROP TABLE scheduled_tasks;
