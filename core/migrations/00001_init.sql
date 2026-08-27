-- +goose Up
-- gcm 基线 schema: 一张节点表 + 一张引用表 + 凭据 + 设置
-- 设计: design.md §2（4 张核心表）

-- ① 节点表: 一切实体（内容 / term / 关系节点），type 区分
CREATE TABLE nodes (
	id         INTEGER PRIMARY KEY,
	type       TEXT    NOT NULL,               -- article / category / person / employment ...
	title      TEXT    NOT NULL DEFAULT '',    -- 显示名投影列（类型级 title 声明映射, 列表/搜索/排序用）
	slug       TEXT    NOT NULL DEFAULT '',    -- URL 段（类型可配置是否用）
	status     INTEGER NOT NULL DEFAULT 0,     -- 发布状态（通用）
	sort       INTEGER NOT NULL DEFAULT 0,
	fields     TEXT    NOT NULL DEFAULT '{}',  -- 类型特有字段（类型系统校验）
	created_at TIMESTAMP NOT NULL,
	updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX idx_nodes_slug ON nodes(slug) WHERE slug <> '';
CREATE INDEX idx_nodes_type ON nodes(type, status, sort);

-- ② 引用表: 无身份引用（引擎内部实现，类型系统只见"引用字段"）
CREATE TABLE edges (
	id         INTEGER PRIMARY KEY,
	from_node  INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	field      TEXT    NOT NULL,               -- 类型定义里的引用字段名
	to_node    INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
	sort       INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP NOT NULL,
	UNIQUE (from_node, field, to_node)
);
CREATE INDEX idx_edges_from ON edges(from_node, field);
CREATE INDEX idx_edges_to   ON edges(to_node, field);

-- ③ 后台凭据（外置，认证语义不进实体）
CREATE TABLE accounts (
	id            INTEGER PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	session_key   TEXT NOT NULL DEFAULT '',
	created_at    TIMESTAMP NOT NULL,
	updated_at    TIMESTAMP NOT NULL
);

-- ④ 设置（引擎内部键值，站点级配置）
CREATE TABLE settings (
	"key"      TEXT NOT NULL PRIMARY KEY,
	value      TEXT NOT NULL DEFAULT '{}',
	updated_at TIMESTAMP NOT NULL
);

-- +goose Down
DROP TABLE edges;
DROP TABLE nodes;
DROP TABLE accounts;
DROP TABLE settings;
