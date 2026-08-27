package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
)

// ── 引用落边（引擎内部 — Create 用） ────────────

// splitRefs 把 fields 分成标量（落节点 fields JSON）与引用（落 edges）—
// 引用判断走类型容器的存储形态（kind 自己说了算）。
func splitRefs(td types.TypeDef, ts *types.Types, fields map[string]any) (map[string]any, map[string]any, error) {
	scalar := map[string]any{}
	refs := map[string]any{}
	for name, v := range fields {
		f, ok := types.FieldByName(td, name)
		if !ok {
			return nil, nil, fmt.Errorf("core: field %q not on type %q", name, td.Name)
		}
		if ts.IsRefKind(f.Kind) {
			refs[name] = v
		} else {
			scalar[name] = v
		}
	}
	return scalar, refs, nil
}

// addEdges 校验 ref 目标（存在 + 类型匹配）并插入边。
func addEdges(tx *dba.SQL, ts *types.Types, td types.TypeDef, from int64, refs map[string]any) error {
	for fieldName, v := range refs {
		// nil = 清空引用（PATCH 语义: 删边已做, 不加新边）
		if v == nil {
			continue
		}
		f, ok := types.FieldByName(td, fieldName)
		if !ok {
			return fmt.Errorf("core: field %q not on type %q", fieldName, td.Name)
		}
		ids, err := refIDs(ts, f, v)
		if err != nil {
			return err
		}
		for _, to := range ids {
			if err := checkTarget(tx, to, f.To); err != nil {
				return fmt.Errorf("core: %q.%s -> %d: %w", td.Name, fieldName, to, err)
			}
			if _, err := tx.Insert("edges", map[string]any{
				"from_node": from, "field": fieldName, "to_node": to, "sort": 0,
				"created_at": time.Now(),
			}).Exec(); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkTarget 目标存在且类型匹配。
func checkTarget(tx *dba.SQL, id int64, wantType string) error {
	typPtr, err := tx.Add(`SELECT type FROM nodes WHERE id = #{1}`, id).FetchOne[string]()
	if err != nil {
		return err
	}
	if typPtr == nil {
		return fmt.Errorf("target %d not found", id)
	}
	if *typPtr != wantType {
		return fmt.Errorf("target %d is type %q, want %q", id, *typPtr, wantType)
	}
	return nil
}

// refIDs 引用字段值 → id 列表（ref 单个包一层, ref[] 原样）。
func refIDs(ts *types.Types, f types.FieldDef, v any) ([]int64, error) {
	k, ok := ts.Kind(f.Kind)
	if !ok {
		return nil, fmt.Errorf("core: unknown kind %q", f.Kind)
	}
	switch k.Class() {
	case types.ClassRef:
		id, err := types.ToID(v)
		if err != nil {
			return nil, fmt.Errorf("core: ref value: %w", err)
		}
		return []int64{id}, nil
	case types.ClassRefList:
		arr, ok := v.([]any)
		if !ok {
			return nil, fmt.Errorf("core: ref[] value: expects array, got %T", v)
		}
		ids := make([]int64, 0, len(arr))
		for i, e := range arr {
			id, err := types.ToID(e)
			if err != nil {
				return nil, fmt.Errorf("core: ref[] value[%d]: %w", i, err)
			}
			ids = append(ids, id)
		}
		return ids, nil
	default:
		return nil, fmt.Errorf("core: %s is not a ref kind", f.Kind)
	}
}

// ErrEdgeNotFound 目标引用不存在。
var ErrEdgeNotFound = errors.New("core: edge not found")

// fieldOnType 字段归属校验: 字段在类型上声明, 返回 (FieldDef, symmetric, err)。
func (s *Service) fieldOnType(typeName, field string) (types.FieldDef, bool, error) {
	td, ok := s.types.Type(typeName)
	if !ok {
		return types.FieldDef{}, false, fmt.Errorf("core: type %q not defined", typeName)
	}
	f, ok := types.FieldByName(td, field)
	if !ok {
		return types.FieldDef{}, false, fmt.Errorf("core: field %q not on type %q", field, typeName)
	}
	if !s.types.IsRefKind(f.Kind) {
		return types.FieldDef{}, false, fmt.Errorf("core: field %q is not a ref kind", f.Name)
	}
	return f, f.Symmetric, nil
}

// AddEdge 手动加边: from 存在 + 字段归属 + to 类型匹配。
func (s *Service) AddEdge(from, to int64, field string, sort int) (int64, error) {
	fromNode, err := s.GetNodeById(from)
	if err != nil {
		return 0, err
	}
	if fromNode == nil {
		return 0, fmt.Errorf("core: addref: from node %d not found", from)
	}
	f, _, err := s.fieldOnType(fromNode.Type, field)
	if err != nil {
		return 0, err
	}
	if err := checkTarget(s.db, to, f.To); err != nil {
		return 0, fmt.Errorf("core: addref: %w", err)
	}
	var id int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		res, err := tx.Insert("edges", map[string]any{
			"from_node": from, "field": field, "to_node": to, "sort": sort,
			"created_at": time.Now(),
		}).Exec()
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// RemoveEdge 删一条引用（按 id）。
func (s *Service) RemoveEdge(id int64) error {
	res, err := s.db.Delete("edges", `id = #{1}`, id).Exec()
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrEdgeNotFound
	}
	return nil
}

// Merge 合并节点: from 的出/入边改指向 to（冲突去重）+ 删 from。
func (s *Service) Merge(from, to int64) error {
	if from == to {
		return errors.New("core: merge: from == to")
	}
	fromNode, err := s.GetNodeById(from)
	if err != nil {
		return err
	}
	if fromNode == nil {
		return ErrNotFound
	}
	toNode, err := s.GetNodeById(to)
	if err != nil {
		return err
	}
	if toNode == nil {
		return ErrNotFound
	}
	return s.db.Transaction(func(tx *dba.SQL) error {
		if _, err := tx.Add(
			`DELETE FROM edges WHERE from_node = #{1} AND EXISTS (
				SELECT 1 FROM edges e2 WHERE e2.from_node = #{2}
				  AND e2.field = edges.field AND e2.to_node = edges.to_node)`,
			from, to).Exec(); err != nil {
			return err
		}
		if _, err := tx.Add(
			`UPDATE edges SET from_node = #{1} WHERE from_node = #{2}`, to, from).Exec(); err != nil {
			return err
		}
		if _, err := tx.Add(
			`DELETE FROM edges WHERE to_node = #{1} AND EXISTS (
				SELECT 1 FROM edges e2 WHERE e2.to_node = #{2}
				  AND e2.field = edges.field AND e2.from_node = edges.from_node)`,
			from, to).Exec(); err != nil {
			return err
		}
		if _, err := tx.Add(
			`UPDATE edges SET to_node = #{1} WHERE to_node = #{2}`, to, from).Exec(); err != nil {
			return err
		}
		if _, err := tx.Add(
			`DELETE FROM edges WHERE from_node = #{1} OR to_node = #{1}`, from).Exec(); err != nil {
			return err
		}
		res, err := tx.Delete("nodes", `id = #{1}`, from).Exec()
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
