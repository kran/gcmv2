// 迁移执行器（goose 封装）— SQL 在 core/migrations/*.sql（embed 库内）。
//
//	引擎迁移:  MigrateUp(db) — 零参数（embed 内置迁移）
//	站点迁移:  NewMigrator(db).Up(fsys, tableName) — 业务表独立版本表
//
// fail loud: 迁移失败 = 拒绝启动（不静默旧结构）。

package core

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/kran/dba"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationSQL 剥离 migrations/ 前缀（goose 需要 FS 根下是 *.sql）。
var migrationSQL = func() fs.FS {
	sub, err := fs.Sub(migrationFS, "migrations")
	if err != nil {
		panic("migrations: embed sub: " + err.Error())
	}
	return sub
}()

// gcmVersionTable 引擎迁移版本表名（与站点业务表迁移错开）。
const gcmVersionTable = "migr_gcm"

// migrateUp 对 db 执行引擎内置迁移（embed core/migrations/*.sql; 版本表 migr_gcm）。
// 返回本次应用数量（0 = 已最新 — 调用方据此判断是否需后续处理）。
func migrateUp(db *dba.SQL) (int, error) {
	return NewMigrator(db).Up(migrationSQL, gcmVersionTable)
}

// MigrateUp 引擎内置迁移（New 之后调用 — 建表）。
func (s *Service) MigrateUp() (int, error) {
	return migrateUp(s.db)
}

// Migrator 站点业务表迁移执行器（gcm 只管自己的表; 站点项目自己的表
// 用独立 goose provider + 独立版本表名 — 两套迁移互不干扰）。
//
// Setup 里跑:
//
//	migrator := core.NewMigrator(site.DB())
//	migrator.Up(myFS, "site_goose_db_version")
type Migrator struct {
	db *dba.SQL
}

// NewMigrator 建迁移执行器。
func NewMigrator(db *dba.SQL) *Migrator {
	return &Migrator{db: db}
}

// Up 执行 fsys 里的全部待应用迁移。tableName 是版本表名（与引擎迁移错开）。
// 返回本次应用的数量。
func (m *Migrator) Up(fsys fs.FS, tableName string) (int, error) {
	opts := []goose.ProviderOption{goose.WithTableName(tableName)}
	provider, err := goose.NewProvider(goose.DialectSQLite3, m.db.Pool().DB, fsys, opts...)
	if err != nil {
		return 0, fmt.Errorf("migrations: provider: %w", err)
	}
	results, err := provider.Up(context.Background())
	if err != nil {
		return 0, fmt.Errorf("migrations: up: %w", err)
	}
	return len(results), nil
}

// UpDir 从本地目录执行迁移（开发期改迁移文件即生效, 不用重新编译 embed）。
func (m *Migrator) UpDir(dir string, tableName string) (int, error) {
	return m.Up(os.DirFS(dir), tableName)
}
