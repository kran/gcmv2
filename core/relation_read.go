package core

import (
	"context"
	"fmt"
	"maps"

	"github.com/kran/gcmv2/types"
)

// EditableNode combines scalar Fields with reference IDs for trusted editing
// and authorization workflows. Node.Fields remains the raw persisted scalar map.
type EditableNode struct {
	Node
	Values Fields `json:"values"`
}

// RefID returns one single-reference target.
func (s *Service) RefID(ctx context.Context, nodeID int64, fieldName string) (int64, bool, error) {
	node, field, err := s.referenceField(ctx, nodeID, fieldName)
	if err != nil {
		return 0, false, err
	}
	kind, _ := s.types.Kind(field.Kind)
	if kind.Class() != types.ClassRef {
		return 0, false, fmt.Errorf("core: %s.%s is not a single ref", node.Type, fieldName)
	}
	ids, err := s.refIDsForField(ctx, nodeID, field)
	if err != nil {
		return 0, false, err
	}
	if len(ids) == 0 {
		return 0, false, nil
	}
	if len(ids) > 1 {
		return 0, false, fmt.Errorf("core: single ref %s.%s has %d edges", node.Type, fieldName, len(ids))
	}
	return ids[0], true, nil
}

// RefIDs returns all targets of one ref[] field in stable edge order.
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

// HasRef reports whether a schema-declared reference contains targetID.
func (s *Service) HasRef(ctx context.Context, nodeID int64, fieldName string, targetID int64) (bool, error) {
	_, field, err := s.referenceField(ctx, nodeID, fieldName)
	if err != nil {
		return false, err
	}
	where := `from_node = #{1} AND field = #{2} AND to_node = #{3}`
	if field.Symmetric || field.Equivalence {
		where = `field = #{2} AND ((from_node = #{1} AND to_node = #{3}) OR (to_node = #{1} AND from_node = #{3}))`
	}
	row, err := s.db.WithCtx(ctx).Add(`SELECT 1 FROM edges WHERE `+where+` LIMIT 1`, nodeID, fieldName, targetID).FetchOne[int]()
	if err != nil {
		return false, err
	}
	return row != nil, nil
}

// FullNode returns one editable projection with scalar and reference values.
func (s *Service) FullNode(ctx context.Context, id int64) (*EditableNode, error) {
	nodes, err := s.FullNodes(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrNotFound
	}
	return nodes[0], nil
}

// FullNodes returns editable projections in the requested ID order.
func (s *Service) FullNodes(ctx context.Context, ids []int64) ([]*EditableNode, error) {
	if len(ids) == 0 {
		return []*EditableNode{}, nil
	}
	nodes, err := s.db.WithCtx(ctx).Add(
		`SELECT * FROM nodes WHERE id IN (#{1|expand})`, ids).FetchList[Node]()
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*EditableNode, len(nodes))
	for i := range nodes {
		editable := &EditableNode{Node: nodes[i], Values: make(Fields, len(nodes[i].Fields))}
		maps.Copy(editable.Values, nodes[i].Fields)
		byID[nodes[i].ID] = editable
	}
	edges, err := s.db.WithCtx(ctx).Add(`SELECT * FROM edges
		WHERE from_node IN (#{1|expand}) OR (symmetric = 1 AND to_node IN (#{1|expand}))
		ORDER BY sort, id`, ids).FetchList[Edge]()
	if err != nil {
		return nil, err
	}
	values := make(map[int64]map[string][]int64)
	for _, edge := range edges {
		if byID[edge.FromNode] != nil {
			appendRefValue(values, edge.FromNode, edge.Field, edge.ToNode)
		}
		if edge.Symmetric && byID[edge.ToNode] != nil {
			appendRefValue(values, edge.ToNode, edge.Field, edge.FromNode)
		}
	}
	for id, editable := range byID {
		td, ok := s.types.Type(editable.Type)
		if !ok {
			return nil, fmt.Errorf("core: type %q not defined", editable.Type)
		}
		for _, field := range td.Fields {
			kind, ok := s.types.Kind(field.Kind)
			if !ok || (kind.Class() != types.ClassRef && kind.Class() != types.ClassRefList) {
				continue
			}
			ids := values[id][field.Name]
			if kind.Class() == types.ClassRef {
				if len(ids) > 1 {
					return nil, fmt.Errorf("core: single ref %s.%s has %d edges", editable.Type, field.Name, len(ids))
				}
				if len(ids) == 1 {
					editable.Values[field.Name] = ids[0]
				}
				continue
			}
			items := make([]any, len(ids))
			for i, targetID := range ids {
				items[i] = targetID
			}
			editable.Values[field.Name] = items
		}
	}
	ordered := make([]*EditableNode, 0, len(nodes))
	for _, id := range ids {
		if node := byID[id]; node != nil {
			ordered = append(ordered, node)
		}
	}
	return ordered, nil
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
