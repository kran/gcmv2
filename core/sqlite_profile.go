package core

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
)

// verifySQLiteProfile 检查 SQLite 连接档位是否满足内核不变量。
//
//	gcm.sqlite  由站点提供连接，档位错了不会立刻报错，而是在并发下静默退化：
//	journal_mode != WAL          读者撞写提交的排他锁；慢读挡住写者 → 请求随机失败
//	busy_timeout = 0            撞锁不等待，立即 SQLITE_BUSY（dba 不做重试）
//	foreign_keys = 0            引用完整性/级联删除失效
//
// 所以启动时校验一次并拒绝启动（fail-loud）。非 SQLite 驱动直接跳过。
// 推荐用 web.Open（它按这些档位拼 DSN）；自己开库的站点按同样档位拼 DSN。
func verifySQLiteProfile(db *dba.SQL) error {
	pool := db.Pool()
	switch pool.DriverName() {
	case "sqlite", "sqlite3":
	default:
		return nil
	}
	var journal string
	if err := pool.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		return fmt.Errorf("core: read sqlite journal_mode: %w", err)
	}
	if !strings.EqualFold(journal, "wal") {
		return fmt.Errorf("core: sqlite journal_mode is %q, want WAL "+
			"(open the database with _pragma=journal_mode(WAL); concurrent reads and writes fail without it)", journal)
	}
	var foreignKeys int64
	if err := pool.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("core: read sqlite foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("core: sqlite foreign_keys is off " +
			"(open the database with _pragma=foreign_keys(1); relation integrity depends on it)")
	}
	var busyTimeout int64
	if err := pool.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("core: read sqlite busy_timeout: %w", err)
	}
	if busyTimeout <= 0 {
		return fmt.Errorf("core: sqlite busy_timeout is %d "+
			"(open the database with _pragma=busy_timeout(5000); locked databases fail immediately without it)", busyTimeout)
	}
	return nil
}
