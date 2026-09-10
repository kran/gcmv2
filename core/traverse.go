package core

import (
	"context"
	"fmt"

	"github.com/kran/gcmv2/types"
)

// Traverse follows a transitive outgoing reference.
func (s *Service) Traverse(ctx context.Context, typeName string, start int64, field string, maxHops int) ([]int64, error) {
	err := s.validateTraversal(ctx, typeName, start, field, maxHops, false)
	if err != nil {
		return nil, err
	}
	return s.walk(ctx, `
		WITH RECURSIVE walk(id, depth, path) AS (
			SELECT to_node, 1, printf(',%d,%d,', from_node, to_node)
			FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, w.depth + 1, w.path || printf('%d,', e.to_node)
			FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
			  AND instr(w.path, printf(',%d,', e.to_node)) = 0
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// Subtree follows a transitive reference in the incoming direction.
func (s *Service) Subtree(ctx context.Context, typeName string, start int64, field string, maxHops int) ([]int64, error) {
	err := s.validateTraversal(ctx, typeName, start, field, maxHops, false)
	if err != nil {
		return nil, err
	}
	return s.walk(ctx, `
		WITH RECURSIVE walk(id, depth, path) AS (
			SELECT from_node, 1, printf(',%d,%d,', to_node, from_node)
			FROM edges WHERE field = #{1} AND to_node = #{2}
			UNION ALL
			SELECT e.from_node, w.depth + 1, w.path || printf('%d,', e.from_node)
			FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
			  AND instr(w.path, printf(',%d,', e.from_node)) = 0
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// Ancestors returns a transitive parent chain in root-to-leaf order.
func (s *Service) Ancestors(ctx context.Context, typeName string, start int64, field string, maxHops int) ([]*Node, error) {
	err := s.validateTraversal(ctx, typeName, start, field, maxHops, false)
	if err != nil {
		return nil, err
	}
	ids, err := s.walk(ctx, `
		WITH RECURSIVE anc(id, depth, path) AS (
			SELECT to_node, 1, printf(',%d,%d,', from_node, to_node)
			FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, a.depth + 1, a.path || printf('%d,', e.to_node)
			FROM edges e JOIN anc a ON e.from_node = a.id
			WHERE e.field = #{1} AND a.depth < #{3}
			  AND instr(a.path, printf(',%d,', e.to_node)) = 0
		)
		SELECT id FROM anc ORDER BY depth DESC`, field, start, maxHops)
	if err != nil {
		return nil, err
	}
	nodes := make([]*Node, 0, len(ids))
	for _, id := range ids {
		node, err := s.GetNodeById(ctx, id)
		if err != nil {
			return nil, err
		}
		if node != nil {
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func (s *Service) walk(ctx context.Context, cte string, args ...any) ([]int64, error) {
	return s.db.WithCtx(ctx).Add(cte, args...).FetchList[int64]()
}

// EquivalenceClass follows an equivalence relation in both directions and
// includes the start Node.
func (s *Service) EquivalenceClass(ctx context.Context, typeName string, start int64, field string, maxHops int) ([]int64, error) {
	err := s.validateTraversal(ctx, typeName, start, field, maxHops, true)
	if err != nil {
		return nil, err
	}
	return s.walk(ctx, `
		WITH RECURSIVE walk(id, depth, path) AS (
			SELECT #{2}, 0, printf(',%d,', #{2})
			UNION ALL
			SELECT CASE WHEN e.from_node = w.id THEN e.to_node ELSE e.from_node END,
				w.depth + 1,
				w.path || printf('%d,', CASE WHEN e.from_node = w.id THEN e.to_node ELSE e.from_node END)
			FROM edges e JOIN walk w ON e.from_node = w.id OR e.to_node = w.id
			WHERE e.field = #{1} AND e.symmetric = 1 AND w.depth < #{3}
			  AND instr(w.path, printf(',%d,', CASE WHEN e.from_node = w.id THEN e.to_node ELSE e.from_node END)) = 0
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

func (s *Service) validateTraversal(ctx context.Context, typeName string, start int64, fieldName string, maxHops int, equivalence bool) error {
	if maxHops <= 0 || maxHops > 100 {
		return fmt.Errorf("core: traversal maxHops must be between 1 and 100")
	}
	field, _, err := s.fieldOnType(typeName, fieldName)
	if err != nil {
		return err
	}
	if equivalence {
		if !field.Equivalence {
			return fmt.Errorf("core: field %s.%s is not an equivalence relation", typeName, fieldName)
		}
	} else if !field.Transitive && !isTreeField(s.types, typeName, fieldName) {
		return fmt.Errorf("core: field %s.%s is not transitive", typeName, fieldName)
	}
	node, err := s.GetNodeById(ctx, start)
	if err != nil {
		return err
	}
	if node == nil || node.ArchivedAt != nil || node.Type != typeName {
		return fmt.Errorf("core: traversal start %d is not an active %s", start, typeName)
	}
	return nil
}

func isTreeField(typeSet *types.Types, typeName, fieldName string) bool {
	tree, ok := typeSet.Tree(typeName)
	return ok && tree.Parent == fieldName
}
