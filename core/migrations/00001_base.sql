-- +goose Up
-- gcm 历史基线 schema（整合旧 00001~00007）。
-- 新数据库也先建立该历史形状，再由后续迁移升级到当前 Node Schema；
-- 不要把本文件当作当前运行时结构。

-- ① 节点表: 一切实体（内容 / term / 关系节点），type 区分
CREATE TABLE IF NOT EXISTS nodes (
	id         INTEGER PRIMARY KEY,
	type       TEXT    NOT NULL,               -- article / category / person / employment ...
	display    TEXT    NOT NULL DEFAULT '',    -- 显示文本固有列（原名 title）
	slug       TEXT    NOT NULL DEFAULT '',    -- URL 段（类型可配置是否用）
	status     INTEGER NOT NULL DEFAULT 0,     -- 发布状态（通用）
	sort       INTEGER NOT NULL DEFAULT 0,
	fields     TEXT    NOT NULL DEFAULT '{}',  -- 类型特有字段（类型系统校验）
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_slug ON nodes(slug) WHERE slug <> '';
CREATE INDEX IF NOT EXISTS idx_nodes_type ON nodes(type, status, sort);

-- ② 引用表: 无身份引用（引擎内部实现，类型系统只见"引用字段"）
CREATE TABLE IF NOT EXISTS edges (
	id         INTEGER PRIMARY KEY,
	from_node  INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	field      TEXT    NOT NULL,               -- 类型定义里的引用字段名
	to_node    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	sort       INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL,
	UNIQUE (from_node, field, to_node)
);
CREATE INDEX IF NOT EXISTS idx_edges_from ON edges(from_node, field);
CREATE INDEX IF NOT EXISTS idx_edges_to   ON edges(to_node, field);

-- ③ 后台凭据（外置，认证语义不进实体）
CREATE TABLE IF NOT EXISTS accounts (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	session_key   TEXT NOT NULL DEFAULT '',
	created_at    TIMESTAMP NOT NULL,
	updated_at    TIMESTAMP NOT NULL
);

-- ④ 设置（引擎内部键值，站点级配置; group/type = 运营元数据）
CREATE TABLE IF NOT EXISTS settings (
	"key"      TEXT NOT NULL PRIMARY KEY,
	value      TEXT NOT NULL DEFAULT '{}',
	group_name TEXT NOT NULL DEFAULT '',
	type       TEXT NOT NULL DEFAULT 'string',
	updated_at TIMESTAMP NOT NULL
);

-- ⑤ 全文索引（应用层同步; rowid = node id）
-- tokenize 容器而已: 中文已由 Go 层切成 bigram
CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    type,
    display,
    body_text,
    tokenize = 'unicode61'
);

-- ⑥ 前台认证: 多登录方式 + 会话。
-- 一个节点（auth 类型）可关联多行登录方式（email/phone/wechat...）;
-- data = 各方式自定义凭证 JSON（password hash / oauth token / session_key）。
CREATE TABLE IF NOT EXISTS auth_methods (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,                -- 认证类型名（nodes.type — 站点可多 auth 类型）
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  method TEXT NOT NULL,              -- 登录方式: email / phone / wechat / github ...
  identifier TEXT NOT NULL,          -- 登录标识: 邮箱 / 手机号 / openid / oauth sub
  data TEXT NOT NULL DEFAULT '{}',   -- 凭据（各方式自定义 JSON）
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(type, method, identifier)   -- 类型内唯一（不同类型互不冲突）
);
CREATE INDEX IF NOT EXISTS idx_auth_methods_node ON auth_methods(node_id);

CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,            -- 随机 32 字节（cookie 值 = Bearer 值 — 双轨）
  node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL,      -- 服务端过期（滑动 — 过半刷新）
  created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_node ON sessions(node_id);

-- +goose Down
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS auth_methods;
DROP TABLE IF EXISTS nodes_fts;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS edges;
DROP TABLE IF EXISTS nodes;
