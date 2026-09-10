package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// addEdges validates and inserts all references from one Node.
func addEdges(tx *dba.SQL, ts *types.Types, td types.TypeDef, from int64, refs map[string]any) error {
	for fieldName, value := range refs {
		if value == nil {
			continue
		}
		field, ok := types.FieldByName(td, fieldName)
		if !ok {
			return fmt.Errorf("core: field %q not on type %q", fieldName, td.Name)
		}
		ids, err := refIDs(ts, field, value)
		if err != nil {
			return err
		}
		seen := make(map[int64]bool, len(ids))
		for position, targetID := range ids {
			if seen[targetID] {
				return fmt.Errorf("%w: %q.%s contains duplicate target %d", ErrRelationCardinality, td.Name, fieldName, targetID)
			}
			seen[targetID] = true
			_, err = insertEdge(tx, ts, td, field, from, targetID, position)
			if err != nil {
				return fmt.Errorf("core: %q.%s -> %d: %w", td.Name, fieldName, targetID, err)
			}
		}
	}
	return nil
}

// checkTarget rejects missing, archived, and wrong-Type targets.
func checkTarget(tx *dba.SQL, id int64, wantType string) error {
	target, err := tx.Add(`SELECT type, archived_at FROM nodes WHERE id = #{1}`, id).FetchOne[struct {
		Type       string     `db:"type"`
		ArchivedAt *time.Time `db:"archived_at"`
	}]()
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("%w: target %d", ErrNotFound, id)
	}
	if target.ArchivedAt != nil {
		return fmt.Errorf("%w: target %d", ErrNodeArchived, id)
	}
	if target.Type != wantType {
		return fmt.Errorf("target %d is type %q, want %q", id, target.Type, wantType)
	}
	return nil
}

func insertEdge(tx *dba.SQL, ts *types.Types, td types.TypeDef, field types.FieldDef, from, to int64, sort int) (int64, error) {
	if err := checkTarget(tx, to, field.To); err != nil {
		return 0, err
	}
	kind, _ := ts.Kind(field.Kind)
	single := kind.Class() == types.ClassRef
	undirected := field.Symmetric || field.Equivalence
	if (undirected || field.Transitive || isTreeParent(td, field.Name)) && from == to {
		return 0, errors.New("self reference is not allowed for algebraic relations")
	}
	if field.Transitive || isTreeParent(td, field.Name) {
		cycle, err := wouldCreateCycle(tx, td.Name, field.Name, from, to)
		if err != nil {
			return 0, err
		}
		if cycle {
			return 0, errors.New("reference would create a cycle")
		}
	}
	if undirected && from > to {
		from, to = to, from
	}
	result, err := tx.Insert("edges", map[string]any{
		"from_node":  from,
		"field":      field.Name,
		"to_node":    to,
		"sort":       sort,
		"single_ref": boolInt(single),
		"symmetric":  boolInt(undirected),
		"created_at": time.Now(),
	}).Exec()
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: edges") ||
			strings.Contains(err.Error(), "symmetric single ref cardinality violation") {
			return 0, fmt.Errorf("%w: %s.%s", ErrRelationCardinality, td.Name, field.Name)
		}
		return 0, err
	}
	return result.LastInsertId()
}

func isTreeParent(td types.TypeDef, field string) bool {
	return td.Capabilities.Tree != nil && td.Capabilities.Tree.Parent == field
}

func wouldCreateCycle(tx *dba.SQL, typeName, field string, from, to int64) (bool, error) {
	found, err := tx.Add(`WITH RECURSIVE reach(id) AS (
		SELECT #{1}
		UNION
		SELECT e.to_node FROM edges e JOIN reach r ON e.from_node = r.id
		JOIN nodes n ON n.id = e.from_node
		WHERE e.field = #{2} AND n.type = #{3}
	)
	SELECT 1 FROM reach WHERE id = #{4} LIMIT 1`, to, field, typeName, from).FetchOne[int]()
	if err != nil {
		return false, err
	}
	return found != nil, nil
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

var (
	// ErrEdgeNotFound means the requested Edge does not exist.
	ErrEdgeNotFound = errors.New("core: edge not found")
	// ErrRequiredReference means an operation would leave a required ref empty.
	ErrRequiredReference = errors.New("core: required reference")
	// ErrRelationCardinality means a ref/ref[] database invariant would be violated.
	ErrRelationCardinality = errors.New("core: relation cardinality violation")
	// ErrDeleteRestricted means incoming references prohibit permanent deletion.
	ErrDeleteRestricted = errors.New("core: delete restricted")
)

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
	return f, f.Symmetric || f.Equivalence, nil
}

// AddEdge manually inserts one schema-validated reference.
func (s *Service) AddEdge(ctx context.Context, from, to int64, fieldName string, sort int) (int64, error) {
	fromNode, err := s.GetNodeById(ctx, from)
	if err != nil {
		return 0, err
	}
	if fromNode == nil {
		return 0, fmt.Errorf("core: addref: %w: source %d", ErrNotFound, from)
	}
	if fromNode.ArchivedAt != nil {
		return 0, fmt.Errorf("core: addref: %w: source %d", ErrNodeArchived, from)
	}
	field, _, err := s.fieldOnType(fromNode.Type, fieldName)
	if err != nil {
		return 0, err
	}
	td, _ := s.types.Type(fromNode.Type)
	var id int64
	err = s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		id, err = insertEdge(tx, s.types, td, field, from, to, sort)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("core: addref: %w", err)
	}
	return id, nil
}

func deleteFieldEdges(tx *dba.SQL, nodeID int64, field types.FieldDef) error {
	where := `from_node = #{1} AND field = #{2}`
	if field.Symmetric || field.Equivalence {
		where = `(from_node = #{1} OR to_node = #{1}) AND field = #{2}`
	}
	_, err := tx.Delete("edges", where, nodeID, field.Name).Exec()
	return err
}

// RemoveEdge removes one reference without violating required cardinality.
func (s *Service) RemoveEdge(ctx context.Context, id int64) error {
	return s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		edge, err := tx.Select("edges", `id = #{1}`, id).FetchOne[Edge]()
		if err != nil {
			return err
		}
		if edge == nil {
			return ErrEdgeNotFound
		}
		source, err := tx.Select("nodes", `id = #{1}`, edge.FromNode).FetchOne[Node]()
		if err != nil {
			return err
		}
		if source == nil {
			return ErrEdgeNotFound
		}
		field, _, err := s.fieldOnType(source.Type, edge.Field)
		if err != nil {
			return err
		}
		if field.Required {
			endpoints := []int64{edge.FromNode}
			if field.Symmetric || field.Equivalence {
				endpoints = append(endpoints, edge.ToNode)
			}
			for _, endpoint := range endpoints {
				where := `from_node = #{1} AND field = #{2}`
				if field.Symmetric || field.Equivalence {
					where = `(from_node = #{1} OR to_node = #{1}) AND field = #{2}`
				}
				count, err := tx.Add(`SELECT COUNT(1) FROM edges WHERE `+where, endpoint, edge.Field).FetchOne[int64]()
				if err != nil {
					return err
				}
				if count == nil || *count <= 1 {
					return fmt.Errorf("%w: %s.%s", ErrRequiredReference, source.Type, edge.Field)
				}
			}
		}
		result, err := tx.Delete("edges", `id = #{1}`, id).Exec()
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrEdgeNotFound
		}
		return nil
	})
}
