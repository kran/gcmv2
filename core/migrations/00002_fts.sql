-- +goose Up
-- 全文索引（应用层同步, 可替换引擎; 默认 SQLite FTS5 + bigram 预分词）
-- rowid = node id（普通存储表, 非外部内容表 — 内容由 Service 写路径同步,
-- 类型 search:true + 已发布 才进索引; 引擎无脑"给什么索引什么"）
CREATE VIRTUAL TABLE IF NOT EXISTS nodes_fts USING fts5(
    type,        -- 类型名（原始值, 过滤用）
    title,       -- 标题（bigram 预分词）
    body_text,   -- 全部可搜标量字段拼接（bigram 预分词）
    tokenize = 'unicode61'  -- 容器而已: 中文已由 Go 层切成 bigram
);

-- +goose Down
DROP TABLE IF EXISTS nodes_fts;
