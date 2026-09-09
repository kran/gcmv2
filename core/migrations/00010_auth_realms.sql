-- +goose Up
-- Realm-bound sessions store only a SHA-256 token hash. Existing plaintext-token
-- sessions are intentionally invalidated during this security migration.
DROP TABLE sessions;

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  realm TEXT NOT NULL,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL
);
CREATE INDEX idx_sessions_node ON sessions(node_id);
CREATE INDEX idx_sessions_realm_node ON sessions(realm, node_id);

-- +goose Down
DROP TABLE sessions;

CREATE TABLE sessions (
  token TEXT PRIMARY KEY,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL
);
CREATE INDEX idx_sessions_node ON sessions(node_id);
