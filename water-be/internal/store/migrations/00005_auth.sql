-- +goose Up
CREATE TABLE auth_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  pin_hash TEXT NOT NULL,
  pin_salt TEXT NOT NULL,
  session_token_hash TEXT NOT NULL DEFAULT '',
  session_expires_at TEXT NOT NULL DEFAULT '',
  session_issued_at TEXT NOT NULL DEFAULT '',
  last_unlocked_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE auth_state;
