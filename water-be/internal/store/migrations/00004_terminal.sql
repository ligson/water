-- +goose Up
CREATE TABLE terminal_profiles (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  name TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL DEFAULT 22,
  username TEXT NOT NULL,
  auth_type TEXT NOT NULL DEFAULT 'password',
  password TEXT NOT NULL DEFAULT '',
  private_key TEXT NOT NULL DEFAULT '',
  passphrase TEXT NOT NULL DEFAULT '',
  default_cwd TEXT NOT NULL DEFAULT '',
  host_fingerprint TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE
);

CREATE INDEX idx_terminal_profiles_workspace_id ON terminal_profiles (workspace_id);

CREATE TABLE terminal_sessions (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  profile_id TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'created',
  cwd TEXT NOT NULL DEFAULT '',
  cols INTEGER NOT NULL DEFAULT 100,
  rows INTEGER NOT NULL DEFAULT 30,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  last_active_at TEXT,
  closed_at TEXT,
  FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE CASCADE,
  FOREIGN KEY (profile_id) REFERENCES terminal_profiles(id) ON DELETE CASCADE
);

CREATE INDEX idx_terminal_sessions_workspace_id ON terminal_sessions (workspace_id);
CREATE INDEX idx_terminal_sessions_profile_id ON terminal_sessions (profile_id);

-- +goose Down
DROP TABLE terminal_sessions;
DROP TABLE terminal_profiles;
