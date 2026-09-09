-- +goose Up
-- Node v0.9：移除 CMS 专用固定列，增加通用并发和归档元数据。
-- 旧列先完整保存在 legacy_node_columns，供各站点的一次性业务迁移读取；
-- 新运行时代码不读取该表，也不同时维护新旧字段。

CREATE TABLE legacy_node_columns (
    node_id INTEGER PRIMARY KEY,
    slug    TEXT NOT NULL DEFAULT '',
    status  INTEGER NOT NULL DEFAULT 0,
    sort    INTEGER NOT NULL DEFAULT 0
);
INSERT INTO legacy_node_columns (node_id, slug, status, sort)
SELECT id, slug, status, sort FROM nodes;

DROP INDEX IF EXISTS idx_nodes_slug;
DROP INDEX IF EXISTS idx_nodes_type;

ALTER TABLE nodes ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE nodes ADD COLUMN archived_at TIMESTAMP;
ALTER TABLE nodes DROP COLUMN slug;
ALTER TABLE nodes DROP COLUMN status;
ALTER TABLE nodes DROP COLUMN sort;

CREATE INDEX idx_nodes_type ON nodes(type, id);
CREATE INDEX idx_nodes_active_type ON nodes(type, id) WHERE archived_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_active_type;
DROP INDEX IF EXISTS idx_nodes_type;

ALTER TABLE nodes ADD COLUMN slug TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN status INTEGER NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN sort INTEGER NOT NULL DEFAULT 0;
UPDATE nodes
SET slug = COALESCE((SELECT slug FROM legacy_node_columns WHERE node_id = nodes.id), ''),
    status = COALESCE((SELECT status FROM legacy_node_columns WHERE node_id = nodes.id), 0),
    sort = COALESCE((SELECT sort FROM legacy_node_columns WHERE node_id = nodes.id), 0);

ALTER TABLE nodes DROP COLUMN archived_at;
ALTER TABLE nodes DROP COLUMN revision;
CREATE UNIQUE INDEX idx_nodes_slug ON nodes(slug) WHERE slug <> '';
CREATE INDEX idx_nodes_type ON nodes(type, status, sort);
DROP TABLE legacy_node_columns;
