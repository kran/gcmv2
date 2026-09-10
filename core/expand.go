package core

import (
	"context"
	"fmt"

	gquery "github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
)

const expandRefLimit = 1000

func (s *Service) nodesByIDs(ctx context.Context, ids []int64) ([]Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := s.db.WithCtx(ctx).Add(
		`SELECT * FROM nodes WHERE archived_at IS NULL AND id IN (#{1|expand})`, ids)
	rows, err := q.FetchList[Node]()
	if err != nil {
		return nil, fmt.Errorf("core: nodes by IDs: %w", err)
	}
	byID := make(map[int64]Node, len(rows))
	for _, node := range rows {
		byID[node.ID] = node
	}
	ordered := make([]Node, 0, len(rows))
	for _, id := range ids {
		if node, ok := byID[id]; ok {
			ordered = append(ordered, node)
		}
	}
	return ordered, nil
}

// Expand loads one Node and applies typed relation paths.
func (s *Service) Expand(ctx context.Context, id int64, paths ...gquery.ExpandPath) (*Node, error) {
	nodes, err := s.ExpandMany(ctx, []int64{id}, paths...)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrNotFound
	}
	return nodes[0], nil
}

// ExpandMany expands relation paths without N+1 queries. Every path hop is
// validated against the current Type; incoming hops include their source Type.
func (s *Service) ExpandMany(ctx context.Context, ids []int64, paths ...gquery.ExpandPath) ([]*Node, error) {
	nodes, err := s.nodesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	roots := make([]*Node, len(nodes))
	for i := range nodes {
		roots[i] = &nodes[i]
	}
	if len(paths) == 0 || len(roots) == 0 {
		return roots, nil
	}
	if len(paths) > gquery.MaxExpandPaths {
		return nil, fmt.Errorf("core: expand exceeds %d paths", gquery.MaxExpandPaths)
	}
	groups := groupNodesByType(roots)
	for _, path := range paths {
		if len(path) == 0 {
			return nil, fmt.Errorf("core: expand path is empty")
		}
		if len(path) > gquery.MaxExpandDepth {
			return nil, fmt.Errorf("core: expand path exceeds depth %d", gquery.MaxExpandDepth)
		}
		for typeName, group := range groups {
			if err := s.expandTyped(ctx, group, typeName, path, 0); err != nil {
				return nil, err
			}
		}
	}
	return roots, nil
}

// AutoExpand returns all outgoing references declared by one Type.
func (s *Service) AutoExpand(typeName string) []gquery.ExpandPath {
	td, ok := s.types.Type(typeName)
	if !ok {
		return nil
	}
	paths := make([]gquery.ExpandPath, 0)
	for _, field := range td.Fields {
		if s.types.IsRefKind(field.Kind) {
			paths = append(paths, gquery.Expand(gquery.Ref(field.Name)))
		}
	}
	return paths
}

type expandRelation struct {
	field      types.FieldDef
	targetType string
	single     bool
	incoming   bool
	undirected bool
	sourceType string
}

func (s *Service) resolveExpandRelation(typeName string, path gquery.Path) (expandRelation, error) {
	switch path.Kind {
	case gquery.PathOutRef:
		field, ok := s.types.Field(typeName, path.Field)
		if !ok || !s.types.IsRefKind(field.Kind) {
			return expandRelation{}, fmt.Errorf("core: expand: %s.%s is not a ref", typeName, path.Field)
		}
		kind, _ := s.types.Kind(field.Kind)
		return expandRelation{
			field: field, targetType: field.To, single: kind.Class() == types.ClassRef,
			undirected: field.Symmetric || field.Equivalence,
		}, nil
	case gquery.PathInRef:
		if path.SourceType == "" {
			return expandRelation{}, fmt.Errorf("core: expand: incoming source type required")
		}
		field, ok := s.types.Field(path.SourceType, path.Field)
		if !ok || !s.types.IsRefKind(field.Kind) || field.To != typeName {
			return expandRelation{}, fmt.Errorf(
				"core: expand: incoming %s.%s does not target %s", path.SourceType, path.Field, typeName)
		}
		return expandRelation{
			field: field, targetType: path.SourceType, incoming: true,
			undirected: field.Symmetric || field.Equivalence, sourceType: path.SourceType,
		}, nil
	default:
		return expandRelation{}, fmt.Errorf("core: expand: path %q is not a relation", path.Field)
	}
}

func (s *Service) expandTyped(
	ctx context.Context,
	nodes []*Node,
	typeName string,
	path gquery.ExpandPath,
	segment int,
) error {
	if len(nodes) == 0 {
		return nil
	}
	relation, err := s.resolveExpandRelation(typeName, path[segment])
	if err != nil {
		return err
	}
	ids := make([]int64, len(nodes))
	for i, node := range nodes {
		if node.Type != typeName {
			return fmt.Errorf("core: expand: node %d is type %q, expected %q", node.ID, node.Type, typeName)
		}
		ids[i] = node.ID
	}
	edgesByNode, err := s.typedEdges(ctx, ids, relation, expandRefLimit)
	if err != nil {
		return err
	}

	targetIDs := make([]int64, 0)
	seen := map[int64]bool{}
	for nodeID, edges := range edgesByNode {
		for _, edge := range edges {
			targetID := relationTargetID(edge, nodeID, relation)
			if !seen[targetID] {
				seen[targetID] = true
				targetIDs = append(targetIDs, targetID)
			}
		}
	}
	targets, err := s.nodesByIDs(ctx, targetIDs)
	if err != nil {
		return err
	}
	byID := make(map[int64]*Node, len(targets))
	for i := range targets {
		if targets[i].Type != relation.targetType {
			return fmt.Errorf(
				"core: expand: target %d is type %q, expected %q",
				targets[i].ID, targets[i].Type, relation.targetType)
		}
		byID[targets[i].ID] = &targets[i]
	}

	key := gquery.ExpandKey(path[segment])
	for _, node := range nodes {
		if node.Expand == nil {
			node.Expand = map[string]any{}
		}
		edges := edgesByNode[node.ID]
		values := make([]*Node, 0, len(edges))
		for _, edge := range edges {
			targetID := relationTargetID(edge, node.ID, relation)
			if target := byID[targetID]; target != nil {
				values = append(values, target)
			}
		}
		if relation.single {
			if len(values) > 1 {
				return fmt.Errorf("core: expand: single ref %s.%s has %d edges", typeName, relation.field.Name, len(values))
			}
			if len(values) == 1 {
				node.Expand[key] = values[0]
			}
			continue
		}
		node.Expand[key] = values
	}

	if segment+1 >= len(path) {
		return nil
	}
	next := make([]*Node, 0, len(targets))
	for i := range targets {
		next = append(next, byID[targets[i].ID])
	}
	return s.expandTyped(ctx, next, relation.targetType, path, segment+1)
}

func (s *Service) typedEdges(
	ctx context.Context,
	ids []int64,
	relation expandRelation,
	limit int,
) (map[int64][]Edge, error) {
	if len(ids) == 0 {
		return map[int64][]Edge{}, nil
	}
	var query string
	var args []any
	if relation.undirected {
		query = `SELECT e.* FROM edges e
			JOIN nodes target ON target.id = CASE
				WHEN e.from_node IN (#{2|expand}) THEN e.to_node ELSE e.from_node END
			WHERE e.field = #{1} AND e.symmetric = 1
			  AND (e.from_node IN (#{2|expand}) OR e.to_node IN (#{2|expand}))
			  AND target.archived_at IS NULL
			ORDER BY e.sort, e.id LIMIT #{3}`
		args = []any{relation.field.Name, ids, limit + 1}
	} else if relation.incoming {
		query = `SELECT e.* FROM edges e JOIN nodes src ON src.id = e.from_node
			WHERE e.field = #{1} AND e.to_node IN (#{2|expand})
			  AND src.type = #{3} AND src.archived_at IS NULL
			ORDER BY e.to_node, e.sort, e.id LIMIT #{4}`
		args = []any{relation.field.Name, ids, relation.sourceType, limit + 1}
	} else {
		query = `SELECT e.* FROM edges e JOIN nodes target ON target.id = e.to_node
			WHERE e.field = #{1} AND e.from_node IN (#{2|expand})
			  AND target.archived_at IS NULL
			ORDER BY e.from_node, e.sort, e.id LIMIT #{3}`
		args = []any{relation.field.Name, ids, limit + 1}
	}
	rows, err := s.db.WithCtx(ctx).Add(query, args...).FetchList[Edge]()
	if err != nil {
		return nil, fmt.Errorf("core: expand edges: %w", err)
	}
	if len(rows) > limit {
		return nil, fmt.Errorf("core: expand %q exceeds %d edges", relation.field.Name, limit)
	}
	out := make(map[int64][]Edge)
	for _, edge := range rows {
		if relation.undirected {
			for _, nodeID := range ids {
				if edge.FromNode == nodeID || edge.ToNode == nodeID {
					out[nodeID] = append(out[nodeID], edge)
				}
			}
			continue
		}
		key := edge.FromNode
		if relation.incoming {
			key = edge.ToNode
		}
		out[key] = append(out[key], edge)
	}
	return out, nil
}

func relationTargetID(edge Edge, nodeID int64, relation expandRelation) int64 {
	if relation.undirected {
		if edge.FromNode == nodeID {
			return edge.ToNode
		}
		return edge.FromNode
	}
	if relation.incoming {
		return edge.FromNode
	}
	return edge.ToNode
}

func groupNodesByType(nodes []*Node) map[string][]*Node {
	groups := make(map[string][]*Node)
	for _, node := range nodes {
		groups[node.Type] = append(groups[node.Type], node)
	}
	return groups
}
