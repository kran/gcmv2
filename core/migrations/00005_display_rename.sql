-- +goose Up
-- nodes.title → display 改名（显示文本固有列 — 消除投影语义）
ALTER TABLE nodes RENAME COLUMN title TO display;
-- FTS 外部内容列映射跟随改名 — 重建（旧索引列名失效）
DROP TABLE IF EXISTS nodes_fts;
CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    type,
    display,
    body_text,
    tokenize = 'unicode61'
);

-- +goose Down
DROP TABLE IF EXISTS nodes_fts;
ALTER TABLE nodes RENAME COLUMN display TO title;
