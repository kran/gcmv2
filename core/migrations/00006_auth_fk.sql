-- +goose Up
-- auth 表加级联外键（SQLite 不能 ALTER 加 FK — 重建表 + 拷贝数据）。
-- 老库 00004 建表时无 FK; 此迁移升级为 REFERENCES nodes(id) ON DELETE CASCADE。

-- auth_methods 重建
CREATE TABLE auth_methods_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  method TEXT NOT NULL,
  identifier TEXT NOT NULL,
  secret TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(type, method, identifier)
);
INSERT INTO auth_methods_new (id, type, node_id, method, identifier, secret, created_at, updated_at)
  SELECT id, type, node_id, method, identifier, secret, created_at, updated_at FROM auth_methods;
DROP TABLE auth_methods;
ALTER TABLE auth_methods_new RENAME TO auth_methods;
CREATE INDEX idx_auth_methods_node ON auth_methods(node_id);

-- sessions 重建
CREATE TABLE sessions_new (
  token TEXT PRIMARY KEY,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL
);
INSERT INTO sessions_new (token, node_id, expires_at, created_at)
  SELECT token, node_id, expires_at, created_at FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_new RENAME TO sessions;
CREATE INDEX idx_sessions_node ON sessions(node_id);

-- +goose Down
DROP TABLE IF EXISTS sessions_new;
DROP TABLE IF EXISTS auth_methods_new;
