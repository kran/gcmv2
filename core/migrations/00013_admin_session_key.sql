-- +goose Up
-- 一个会话键只属于一个管理员。多管理员下"按 cookie 找到本人"必须是确定的一行 ——
-- 这条不变式交给数据库，而不是靠代码里"表里只有一行"的假设。
-- 空键（未登录/已登出）可以重复，所以用部分唯一索引。
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_session_key
    ON accounts(session_key) WHERE session_key <> '';

-- +goose Down
DROP INDEX IF EXISTS idx_accounts_session_key;
