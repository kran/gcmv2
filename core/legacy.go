package core

import (
	"context"
	"log/slog"

	"github.com/kran/dba"
)

// dropLegacyArchive 是一次性迁移：v0.9 之后"归档"不再属于内核（改由项目层用状态字段
// 表达），所以 archived_at 这一列要删掉。删列必须放在 syncSchemaIndexes 之后 ——
// 声明式索引（gcm_schema_*）在那一步已经被整批删除重建（新定义里没有 archived_at），
// 否则 SQLite 会因为还有索引引用该列而拒绝 DROP COLUMN。
//
// 如果库里有归档过的行，删列会让它们重新可见：这里先数出来并告警，让运维知道要处理。
// 所有库升级完成后（v1）可以连同这个函数一起删掉。
func (s *Service) dropLegacyArchive(ctx context.Context) error {
	return s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		has, err := tx.Add(
			`SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name = 'archived_at'`).FetchOne[int]()
		if err != nil {
			return err
		}
		if has == nil || *has == 0 {
			return nil
		}
		archived, err := tx.Add(`SELECT COUNT(*) FROM nodes WHERE archived_at IS NOT NULL`).FetchOne[int]()
		if err != nil {
			return err
		}
		if archived != nil && *archived > 0 {
			slog.Warn("core: archived nodes become visible again (archive column removed)",
				"count", *archived)
		}
		if _, err := tx.Add(`DROP INDEX IF EXISTS idx_nodes_active_type`).Exec(); err != nil {
			return err
		}
		_, err = tx.Add(`ALTER TABLE nodes DROP COLUMN archived_at`).Exec()
		return err
	})
}
