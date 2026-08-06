-- +goose Up
CREATE TABLE providers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  base_url TEXT NOT NULL,
  model TEXT NOT NULL,
  api_key TEXT NOT NULL DEFAULT '',
  is_default INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  timeout_ms INTEGER NOT NULL DEFAULT 30000,
  max_retries INTEGER NOT NULL DEFAULT 2,
  stream_idle_timeout_ms INTEGER NOT NULL DEFAULT 60000,
  headers_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_providers_default ON providers (is_default) WHERE is_default = 1;

CREATE TABLE workspaces (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  root_path TEXT NOT NULL,
  default_provider_id TEXT,
  permission_mode TEXT NOT NULL DEFAULT 'request_approval',
  trusted INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_opened_at TEXT,
  FOREIGN KEY (default_provider_id) REFERENCES providers(id) ON DELETE SET NULL
);

CREATE TABLE workspace_external_paths (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  path TEXT NOT NULL,
  path_type TEXT NOT NULL,
  access_mode TEXT NOT NULL,
  source_task_id TEXT,
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE INDEX idx_workspace_external_paths_workspace_id ON workspace_external_paths (workspace_id);

CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  title TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'created',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  archived_at TEXT,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE INDEX idx_tasks_workspace_id ON tasks (workspace_id);

CREATE TABLE turns (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  sequence INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'created',
  user_input TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  completed_at TEXT,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_turns_task_sequence ON turns (task_id, sequence);

CREATE TABLE events (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  workspace_id TEXT,
  task_id TEXT,
  turn_id TEXT,
  sequence INTEGER NOT NULL DEFAULT 0,
  type TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (turn_id) REFERENCES turns(id) ON DELETE CASCADE
);

CREATE INDEX idx_events_task_turn_sequence ON events (task_id, turn_id, sequence);

CREATE TABLE approvals (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  task_id TEXT,
  turn_id TEXT,
  action_type TEXT NOT NULL,
  target TEXT NOT NULL,
  risk_summary TEXT NOT NULL DEFAULT '',
  expected_impact TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'pending',
  requested_at TEXT NOT NULL,
  resolved_at TEXT,
  decision_message TEXT NOT NULL DEFAULT '',
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE,
  FOREIGN KEY (turn_id) REFERENCES turns(id) ON DELETE CASCADE
);

CREATE INDEX idx_approvals_workspace_status ON approvals (workspace_id, status);

CREATE TABLE file_summaries (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  path TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  language TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  symbols_json TEXT NOT NULL DEFAULT '[]',
  imports_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_file_summaries_workspace_path ON file_summaries (workspace_id, path);

CREATE TABLE workspace_indexes (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  index_type TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE INDEX idx_workspace_indexes_workspace_type ON workspace_indexes (workspace_id, index_type);

CREATE TABLE task_summaries (
  id TEXT PRIMARY KEY,
  task_id TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  summary TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX idx_task_summaries_task_id ON task_summaries (task_id);

CREATE TABLE pinned_contexts (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  task_id TEXT,
  context_type TEXT NOT NULL,
  target TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
);

CREATE INDEX idx_pinned_contexts_workspace_task ON pinned_contexts (workspace_id, task_id);

-- +goose Down
DROP TABLE pinned_contexts;
DROP TABLE task_summaries;
DROP TABLE workspace_indexes;
DROP TABLE file_summaries;
DROP TABLE approvals;
DROP TABLE events;
DROP TABLE turns;
DROP TABLE tasks;
DROP TABLE workspace_external_paths;
DROP TABLE workspaces;
DROP TABLE providers;
