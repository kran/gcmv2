package core

import (
	"context"
	"fmt"

	"github.com/kran/gcmv2/types"
)

// RefIDs returns all targets of one refs field in stable edge order.
func (s *Service) RefIDs(ctx context.Context, nodeID int64, fieldName string) ([]int64, error) {
	node, field, err := s.referenceField(ctx, nodeID, fieldName)
	if err != nil {
		return nil, err
	}
	kind, _ := s.types.Kind(field.Kind)
	if kind.Class() != types.ClassRefList {
		return nil, fmt.Errorf("core: %s.%s is not a ref list", node.Type, fieldName)
	}
	return s.refIDsForField(ctx, nodeID, field)
}

// setRefValue 把引用 id 注入 Fields（Fields 为空时先分配）。
func setRefValue(n *Node, name string, v any) {
	if n.Fields == nil {
		n.Fields = Fields{}
	}
	n.Fields[name] = v
}

// GetNodesByIDs 按 id 批量读（Fields 完整, 按请求的 ID 顺序返回）。
func (s *Service) GetNodesByIDs(ctx context.Context, ids []int64) ([]*Node, error) {
	if len(ids) == 0 {
		return []*Node{}, nil
	}
	out, err := s.nodesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if err := s.hydrateFields(ctx, out); err != nil {
		return nil, err
	}
	byID := make(map[int64]*Node, len(out))
	for i := range out {
		byID[out[i].ID] = &out[i]
	}
	// nodesByIDs 不保证顺序，这里按请求的 ID 顺序摆放。
	ordered := make([]*Node, 0, len(out))
	for _, id := range ids {
		if node := byID[id]; node != nil {
			ordered = append(ordered, node)
		}
	}
	return ordered, nil
}

// hydrateFields 就地补全引用值：把这些节点的引用 id 注入各自的 Fields（Fields 为空时分配）。
// 这是"读节点 = 完整 Fields"的唯一实现 —— 每个读出口都过这里（内部, 不对外）。
func (s *Service) hydrateFields(ctx context.Context, nodes []Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(nodes))
	index := make(map[int64]int, len(nodes))
	for i := range nodes {
		ids = append(ids, nodes[i].ID)
		index[nodes[i].ID] = i
	}
	// 一次覆盖整批（含对称关系的反向边）：SQL 次数与节点数无关。
	edges, err := s.db.WithCtx(ctx).Add(`SELECT * FROM edges
		WHERE from_node IN (#{1|expand}) OR (symmetric = 1 AND to_node IN (#{1|expand}))
		ORDER BY sort, id`, ids).FetchList[Edge]()
	if err != nil {
		return err
	}
	values := make(map[int64]map[string][]int64)
	for _, edge := range edges {
		if _, ok := index[edge.FromNode]; ok {
			appendRefValue(values, edge.FromNode, edge.Field, edge.ToNode)
		}
		if edge.Symmetric {
			if _, ok := index[edge.ToNode]; ok {
				appendRefValue(values, edge.ToNode, edge.Field, edge.FromNode)
			}
		}
	}
	for id, i := range index {
		n := &nodes[i]
		td, ok := s.types.Type(n.Type)
		if !ok {
			return fmt.Errorf("core: type %q not defined", n.Type)
		}
		for _, field := range td.Fields {
			kind, ok := s.types.Kind(field.Kind)
			if !ok || (kind.Class() != types.ClassRef && kind.Class() != types.ClassRefList) {
				continue
			}
			refs := values[id][field.Name]
			if kind.Class() == types.ClassRef {
				if len(refs) > 1 {
					return fmt.Errorf("core: single ref %s.%s has %d edges", n.Type, field.Name, len(refs))
				}
				if len(refs) == 1 {
					setRefValue(n, field.Name, refs[0])
				}
				continue
			}
			// 引用 id 用自己的类型，不用 []any：读出来的值必须能原样写回。
			items := make([]int64, len(refs))
			copy(items, refs)
			setRefValue(n, field.Name, items)
		}
	}
	return nil
}

func (s *Service) referenceField(ctx context.Context, nodeID int64, fieldName string) (*Node, types.FieldDef, error) {
	node, err := s.db.WithCtx(ctx).Select("nodes", `id = #{1}`, nodeID).FetchOne[Node]()
	if err != nil {
		return nil, types.FieldDef{}, err
	}
	if node == nil {
		return nil, types.FieldDef{}, ErrNotFound
	}
	field, ok := s.types.Field(node.Type, fieldName)
	if !ok || !s.types.IsRefKind(field.Kind) {
		return nil, types.FieldDef{}, fmt.Errorf("core: %s.%s is not a ref", node.Type, fieldName)
	}
	return node, field, nil
}

func (s *Service) refIDsForField(ctx context.Context, nodeID int64, field types.FieldDef) ([]int64, error) {
	if field.Symmetric || field.Equivalence {
		rows, err := s.db.WithCtx(ctx).Add(`SELECT CASE WHEN from_node = #{1} THEN to_node ELSE from_node END
			FROM edges WHERE field = #{2} AND symmetric = 1
			AND (from_node = #{1} OR to_node = #{1}) ORDER BY sort, id`, nodeID, field.Name).FetchList[int64]()
		return rows, err
	}
	return s.db.WithCtx(ctx).Add(`SELECT to_node FROM edges
		WHERE from_node = #{1} AND field = #{2} ORDER BY sort, id`, nodeID, field.Name).FetchList[int64]()
}

func appendRefValue(values map[int64]map[string][]int64, nodeID int64, field string, targetID int64) {
	if values[nodeID] == nil {
		values[nodeID] = make(map[string][]int64)
	}
	values[nodeID][field] = append(values[nodeID][field], targetID)
}
