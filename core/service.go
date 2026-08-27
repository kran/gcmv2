package core

import (
	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite" // sqlite driver 注册
)

// Service 核心引擎 — 每站点一个实例, 绑定本站 db + 本站类型系统。
// 节点 CRUD 与引用落边是一个事务（ref 字段值进 edges, fields 只存标量）。
type Service struct {
	db         *dba.SQL
	types      *types.Types
	hooks      *HookBus
	lispFuncsC map[string]LispFuncC // Lisp filter 站点扩展函数
	search     SearchIndex          // 全文检索引擎（默认 FTS5; SetSearchIndex 可换）
}

// New 建引擎: 定义标准 hook 事件。
func New(db *dba.SQL, ts *types.Types) *Service {
	s := &Service{
		db:         db,
		types:      ts,
		hooks:      NewHookBus(),
		lispFuncsC: map[string]LispFuncC{},
	}

	//define hooks
	err := s.hooks.Define(
		HookSpec{Name: HookNodeBeforeCreate, Proto: func(*dba.SQL, *Node) error { return nil }},
		HookSpec{Name: HookNodeAfterCreate, Proto: func(*dba.SQL, *Node) error { return nil }},
		HookSpec{Name: HookNodeBeforeUpdate, Proto: func(*dba.SQL, *NodePatch) error { return nil }},
		HookSpec{Name: HookNodeAfterUpdate, Proto: func(*dba.SQL, *Node) error { return nil }},
		HookSpec{Name: HookNodeBeforeDelete, Proto: func(*dba.SQL, int64) error { return nil }},
		HookSpec{Name: HookNodeAfterDelete, Proto: func(*dba.SQL, int64) error { return nil }},
	)
	if err != nil {
		panic("core: define standard hooks: " + err.Error())
	}

	s.MigrateUp()
	s.initSearch()
	return s
}

// Hooks 站点级 hook 总线。
func (s *Service) Hooks() *HookBus { return s.hooks }

// Types 类型系统。
func (s *Service) Types() *types.Types { return s.types }

// DB 底层数据库句柄（逃生舱）。
func (s *Service) DB() *dba.SQL { return s.db }

// Migrator 站点业务迁移执行器（引擎 db; 独立版本表与引擎迁移错开）。
func (s *Service) Migrator() *Migrator {
	return NewMigrator(s.db)
}
