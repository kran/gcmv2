-- +goose Up
-- auth_methods: secret → data JSON（各方式自定义凭证）。
-- 老数据 secret（原本是 password hash 或 session_key）— 迁移为 {"secret": <原值>} 保留。
CREATE TABLE auth_methods_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  method TEXT NOT NULL,
  identifier TEXT NOT NULL,
  data TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(type, method, identifier)
);
INSERT INTO auth_methods_new (id, type, node_id, method, identifier, data, created_at, updated_at)
  SELECT id, type, node_id, method, identifier, json_object('secret', secret), created_at, updated_at FROM auth_methods;
DROP TABLE auth_methods;
ALTER TABLE auth_methods_new RENAME TO auth_methods;
CREATE INDEX idx_auth_methods_node ON auth_methods(node_id);

-- +goose Down
DROP TABLE IF EXISTS auth_methods_new;
