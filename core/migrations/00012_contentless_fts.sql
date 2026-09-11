-- +goose Up
-- 全文索引改为 contentless（content=''）: 旧表把 bigram 后的正文又存了一份
-- （中文 bigram 放大约 6 倍, 实测占库体积一半以上）, 而搜索只用 rowid + bm25 排名,
-- 不需要回读原文。contentless_delete=1 允许 DELETE（同步与重建都依赖它）。
-- 迁移后索引是空的, Open 检测到本次有迁移会重建一次。
DROP TABLE IF EXISTS nodes_fts;

CREATE VIRTUAL TABLE nodes_fts USING fts5(
    type,
    display,
    body_text,
    tokenize = 'unicode61',
    content = '',
    contentless_delete = 1
);

-- +goose Down
DROP TABLE IF EXISTS nodes_fts;

CREATE VIRTUAL TABLE nodes_fts USING fts5(
    type,
    display,
    body_text,
    tokenize = 'unicode61'
);
