package core

// DB 读写（dba.SQL 手写 — Node 是值模型, 不用 Dao 泛型）。
// 列白名单防注入; fields 一律 json_patch merge。

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite" // sqlite driver 注册
)

var (
	// ErrNotFound 目标节点不存在。
	ErrNotFound = errors.New("core: node not found")
	// ErrRevisionConflict 节点在客户端读取后已被其他写入修改。
	ErrRevisionConflict = errors.New("core: node revision conflict")
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

// GetNodeByAddress 按 addressable capability 的全局地址查节点。
func (s *Service) GetNodeByAddress(address string) (*Node, error) {
	if address == "" {
		return nil, nil
	}
	conditions := make([]string, 0)
	args := make([]any, 0)
	for _, typeName := range s.types.Names() {
		capability, ok := s.types.Addressable(typeName)
		if !ok {
			continue
		}
		start := len(args) + 1
		conditions = append(conditions, fmt.Sprintf(
			`(type = #{%d} AND json_extract(fields, #{%d}) = #{%d})`,
			start, start+1, start+2))
		args = append(args, typeName, "$."+capability.Field, address)
	}
	if len(conditions) == 0 {
		return nil, nil
	}
	query := `SELECT * FROM nodes WHERE archived_at IS NULL AND (` + strings.Join(conditions, " OR ") + `) LIMIT 1`
	n, err := s.db.Add(query, args...).FetchOne[Node]()
	if err != nil {
		return nil, err
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
	fields, err := s.types.ApplyDefaults(n.Type, n.Fields)
	if err != nil {
		return 0, err
	}
	if err := s.types.ValidateFields(n.Type, fields); err != nil {
		return 0, err
	}
	if n.Display == "" {
		return 0, errors.New("core: create: display required")
	}
	// 内部拷贝，不触碰调用方。BeforeCreate 可以补充字段，因此事务内会
	// 再次校验并在校验后拆分引用。
	m := *n
	m.ID = 0
	m.Fields = Fields(fields)
	m.Revision = 1
	m.ArchivedAt = nil
	now := time.Now()
	m.CreatedAt = now
	m.UpdatedAt = now

	var id int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		err := s.hooks.Fire(HookNodeBeforeCreate, tx, &m)
		if err != nil {
			return err
		}
		if m.Type != td.Name {
			return errors.New("core: create: type is immutable")
		}
		if m.Display == "" {
			return errors.New("core: create: display required")
		}
		m.ID = 0
		m.Revision = 1
		m.ArchivedAt = nil
		m.CreatedAt = now
		m.UpdatedAt = now
		if err = s.types.ValidateFields(m.Type, m.Fields); err != nil {
			return err
		}
		scalar, refs, err := splitRefs(td, s.types, m.Fields)
		if err != nil {
			return err
		}
		m.Fields = scalar
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
	if patch.Display == nil && len(patch.Fields) == 0 {
		return nil
	}
	if patch.Revision == nil || *patch.Revision <= 0 {
		return errors.New("core: patch: revision required")
	}
	td, ok := s.types.Type(existing.Type)
	if !ok {
		return fmt.Errorf("core: type %q not defined", existing.Type)
	}

	return s.db.Transaction(func(tx *dba.SQL) error {
		err := s.hooks.Fire(HookNodeBeforeUpdate, tx, patch)
		if err != nil {
			return err
		}
		if err = s.types.ValidatePatchFields(existing.Type, patch.Fields); err != nil {
			return err
		}

		scalarPatch := Fields{}
		refPatch := map[string]any{}
		for name, value := range patch.Fields {
			field, ok := types.FieldByName(td, name)
			if !ok {
				return fmt.Errorf("core: field %q not on type %q", name, existing.Type)
			}
			if s.types.IsRefKind(field.Kind) {
				refPatch[name] = value
			} else {
				scalarPatch[name] = value
			}
		}

		cols := map[string]any{}
		if patch.Display != nil {
			if *patch.Display == "" {
				return errors.New("core: patch: display required")
			}
			cols["display"] = *patch.Display
		}
		if len(scalarPatch) > 0 {
			body, err := json.Marshal(scalarPatch)
			if err != nil {
				return fmt.Errorf("core: patch fields: %w", err)
			}
			cols["fields"] = dba.Expr(`json_patch(fields, #{1})`, string(body))
		}
		if len(cols) == 0 && len(refPatch) == 0 {
			return nil
		}

		cols["updated_at"] = time.Now()
		cols["revision"] = dba.Expr(`revision + 1`)
		result, err := tx.Update("nodes", cols, `id = #{1} AND revision = #{2}`, id, *patch.Revision).Exec()
		if err != nil {
			return err
		}
		updatedRows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updatedRows == 0 {
			return ErrRevisionConflict
		}

		for name := range refPatch {
			_, err := tx.Add(`DELETE FROM edges WHERE from_node = #{1} AND field = #{2}`, id, name).Exec()
			if err != nil {
				return err
			}
		}
		if len(refPatch) > 0 {
			err = addEdges(tx, s.types, td, id, refPatch)
			if err != nil {
				return err
			}
		}

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
