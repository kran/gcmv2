package core

// DB 读写（dba.SQL 手写 — Node 是值模型, 不用 Dao 泛型）。
// 列白名单防注入; fields 一律 json_patch merge。

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite" // sqlite driver 注册
)

// ErrNotFound 目标节点不存在。
var ErrNotFound = errors.New("core: node not found")

// 发布状态。
const (
	StatusDraft     = 0
	StatusPublished = 1
)

// ── 读 ────────────────────────────────────────

// GetNodeById 按 id 取节点; 不存在返回 (nil, nil)。
func (s *Service) GetNodeById(id int64) (*Node, error) {
	n, err := s.db.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	return n, nil
}

// GetNodeBySlug 按 slug 取节点; 不存在返回 (nil, nil)。空 slug 永不命中。
func (s *Service) GetNodeBySlug(slug string) (*Node, error) {
	if slug == "" {
		return nil, nil
	}
	n, err := s.db.Select("nodes", `slug = #{1}`, slug).FetchOne[Node]()
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	return n, nil
}

// ── 写: Create ─────────────────────────────────

// CreateNode 建节点: 校验 → 事务（BeforeCreate → INSERT → ref 落边 → AfterCreate）。
// 返回新节点 ID; 不修改调用方传入的 Node。未知字段直接报错，避免拼写错误和
// 客户端/Schema 漂移被静默吞掉。
func (s *Service) CreateNode(n *Node) (int64, error) {
	if n == nil {
		return 0, errors.New("core: create: nil node")
	}
	td, ok := s.types.Type(n.Type)
	if !ok {
		return 0, fmt.Errorf("core: type %q not defined", n.Type)
	}
	if err := s.types.ValidateFields(n.Type, n.Fields); err != nil {
		return 0, err
	}
	if err := s.validateSlug(n.Slug, 0); err != nil {
		return 0, err
	}
	if n.Display == "" {
		return 0, errors.New("core: create: display required")
	}
	// 内部拷贝（不触碰调用方）: Fields 剥 ref
	m := *n
	scalar, refs, err := splitRefs(td, s.types, m.Fields)
	if err != nil {
		return 0, err
	}
	m.Fields = scalar // ref 剥掉后直接赋回 — Fields.Value 自动 JSON
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now

	var id int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		if err := s.hooks.Fire(HookNodeBeforeCreate, tx, &m); err != nil {
			return err
		}
		res, err := tx.Insert("nodes", &m).Exec()
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		m.ID = id
		if err := addEdges(tx, s.types, td, id, refs); err != nil {
			return err
		}
		return s.hooks.Fire(HookNodeAfterCreate, tx, &m)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ── 写: Patch（差量） ──────────────────────────

// PatchNode 差量更新: 非 nil 列（dba.Update map）+ fields json_patch merge。
// 空 patch（全 nil + fields 空）→ 零 UPDATE（幂等）。
func (s *Service) PatchNode(id int64, patch *NodePatch) error {
	if patch == nil {
		return errors.New("core: patch: nil patch")
	}
	existing, err := s.GetNodeById(id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}
	// slug 合规 + 查重（排除自身）
	if patch.Slug != nil {
		if err := s.validateSlug(*patch.Slug, id); err != nil {
			return err
		}
	}
	// ref 字段分离: patch.Fields 里属于引用（类型定义判断）→ 走边重建（不进 json_patch）
	td, ok := s.types.Type(existing.Type)
	if !ok {
		return fmt.Errorf("core: type %q not defined", existing.Type)
	}
	if err := s.types.ValidatePatchFields(existing.Type, patch.Fields); err != nil {
		return err
	}
	// ref/标量分离 — 用局部变量（不触碰调用方的 patch）
	scalarPatch := Fields{}
	refPatch := map[string]any{}
	for name, v := range patch.Fields {
		f, ok := types.FieldByName(td, name)
		if !ok {
			return fmt.Errorf("core: field %q not on type %q", name, existing.Type)
		}
		if s.types.IsRefKind(f.Kind) {
			refPatch[name] = v
		} else {
			scalarPatch[name] = v
		}
	}
	// 构建 cols（用局部 scalarPatch — 不写回 patch.Fields）
	cols := map[string]any{}
	if patch.Slug != nil {
		cols["slug"] = *patch.Slug
	}
	if patch.Status != nil {
		cols["status"] = *patch.Status
	}
	if patch.Sort != nil {
		cols["sort"] = *patch.Sort
	}
	if patch.Display != nil {
		if *patch.Display == "" {
			return errors.New("core: patch: display required")
		}
		cols["display"] = *patch.Display
	}
	if len(scalarPatch) > 0 {
		b, _ := json.Marshal(scalarPatch)
		cols["fields"] = dba.Expr(`json_patch(fields, #{1})`, string(b))
	}
	// 空 patch 判空 — 先于 hook（无变化不触发 — P3）
	if len(cols) == 0 && len(refPatch) == 0 {
		return nil
	}
	return s.db.Transaction(func(tx *dba.SQL) error {
		// BeforeUpdate 传 patch（站点审计"改了什么" — P8）
		if err := s.hooks.Fire(HookNodeBeforeUpdate, tx, patch); err != nil {
			return err
		}
		// 列 + fields(Expr) + updated_at — dba.Update 一个搞定
		cols["updated_at"] = time.Now()
		if _, err := tx.Update("nodes", cols, "id = #{1}", id).Exec(); err != nil {
			return err
		}
		// ref 字段: 删旧边重建（差量 — 只动出现的字段）
		if len(refPatch) > 0 {
			for name := range refPatch {
				if _, err := tx.Add(
					`DELETE FROM edges WHERE from_node = #{1} AND field = #{2}`, id, name).Exec(); err != nil {
					return err
				}
			}
			if err := addEdges(tx, s.types, td, id, refPatch); err != nil {
				return err
			}
		}
		// AfterUpdate: 事务内重读完整 Node（搜索同步 + 站点拿最终态 —
		// 不能用 s.GetNodeById（s.db 读不到未提交））
		updated, err := tx.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
		if err != nil {
			return err
		}
		if updated == nil {
			return fmt.Errorf("core: update: node %d vanished in tx", id)
		}
		return s.hooks.Fire(HookNodeAfterUpdate, tx, updated)
	})
}

// ── 写: Delete ────────────────────────────────

// DeleteNode 删节点: 显式清全部出/入引用 + 删节点（事务）。
func (s *Service) DeleteNode(id int64) error {
	return s.db.Transaction(func(tx *dba.SQL) error {
		if err := s.hooks.Fire(HookNodeBeforeDelete, tx, id); err != nil {
			return err
		}
		if _, err := tx.Delete("edges", `from_node = #{1} OR to_node = #{1}`, id).Exec(); err != nil {
			return err
		}
		res, err := tx.Delete("nodes", `id = #{1}`, id).Exec()
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return s.hooks.Fire(HookNodeAfterDelete, tx, id)
	})
}

// validateSlug 合规 + 查重（排除自身）— Create/Patch 共用。
// 空 slug 跳过（合法 — 清空语义）; selfID 为自身 id（Create 传 0 = 无自身）。
func (s *Service) validateSlug(slug string, selfID int64) error {
	if slug == "" {
		return nil
	}
	if !types.ValidSlug(slug) {
		return fmt.Errorf("core: invalid slug %q (must start with letter, only letters/digits/_/-, no consecutive --)", slug)
	}
	other, err := s.GetNodeBySlug(slug)
	if err != nil {
		return err
	}
	if other != nil && other.ID != selfID {
		return fmt.Errorf("core: slug %q already in use by node %d", slug, other.ID)
	}
	return nil
}
