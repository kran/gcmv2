-- +goose Up
-- 前台用户认证: 多登录方式 + 会话。
-- 一个节点（auth 类型）可关联多行登录方式（email/phone/wechat...）;
-- 会话 node 级 — 任何方式登录进同一会话（token 双轨: cookie + Bearer）。
CREATE TABLE auth_methods (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,                -- 认证类型名（nodes.type — 站点可多 auth 类型）
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,  -- 关联节点（级联删）
  method TEXT NOT NULL,              -- 登录方式: email / phone / wechat / github ...
  identifier TEXT NOT NULL,          -- 登录标识: 邮箱 / 手机号 / openid / oauth sub
  secret TEXT NOT NULL,  -- 凭据(00007 迁移为 data JSON)
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(type, method, identifier)   -- 类型内唯一（不同类型互不冲突）
);
CREATE INDEX idx_auth_methods_node ON auth_methods(node_id);

CREATE TABLE sessions (
  token TEXT PRIMARY KEY,            -- 随机 32 字节（cookie 值 = Bearer 值 — 双轨）
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,  -- 会话主体（级联删）
  expires_at DATETIME NOT NULL,      -- 服务端过期（滑动 — 过半刷新）
  created_at DATETIME NOT NULL
);
CREATE INDEX idx_sessions_node ON sessions(node_id);

-- +goose Down
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS auth_methods;
