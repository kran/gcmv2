package core

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/kran/gcmv2/types"
)

// RelationIssueKind classifies a detected relation integrity violation.
type RelationIssueKind string

const (
	RelationDanglingSource     RelationIssueKind = "dangling_source"
	RelationDanglingTarget     RelationIssueKind = "dangling_target"
	RelationUnknownField       RelationIssueKind = "unknown_field"
	RelationTargetTypeMismatch RelationIssueKind = "target_type_mismatch"
	RelationCardinality        RelationIssueKind = "cardinality"
	RelationDuplicate          RelationIssueKind = "duplicate"
	RelationRequiredMissing    RelationIssueKind = "required_missing"
	RelationArchivedRequired   RelationIssueKind = "archived_required_target"
	RelationMetadataMismatch   RelationIssueKind = "metadata_mismatch"
	RelationNonCanonical       RelationIssueKind = "non_canonical"
	RelationCycle              RelationIssueKind = "cycle"
)

// RelationIssue identifies one invalid Edge or missing required relationship.
type RelationIssue struct {
	Kind       RelationIssueKind `json:"kind"`
	EdgeID     int64             `json:"edge_id,omitempty"`
	NodeID     int64             `json:"node_id,omitempty"`
	NodeType   string            `json:"node_type,omitempty"`
	Field      string            `json:"field,omitempty"`
	TargetID   int64             `json:"target_id,omitempty"`
	TargetType string            `json:"target_type,omitempty"`
	Message    string            `json:"message"`
}

// RelationReport is read-only; repairs must be explicit operations.
type RelationReport struct {
	Issues []RelationIssue `json:"issues"`
}

// OK reports whether no integrity violations were found.
func (r RelationReport) OK() bool { return len(r.Issues) == 0 }

// CheckRelations scans persisted Edges against the current Schema without
// mutating data.
func (s *Service) CheckRelations(ctx context.Context) (RelationReport, error) {
	nodes, err := s.db.WithCtx(ctx).Add(`SELECT * FROM nodes ORDER BY id`).FetchList[Node]()
	if err != nil {
		return RelationReport{}, err
	}
	edges, err := s.db.WithCtx(ctx).Add(`SELECT * FROM edges ORDER BY id`).FetchList[Edge]()
	if err != nil {
		return RelationReport{}, err
	}
	nodeByID := make(map[int64]Node, len(nodes))
	for _, node := range nodes {
		nodeByID[node.ID] = node
	}

	report := RelationReport{Issues: make([]RelationIssue, 0)}
	counts := make(map[relationCountKey]int)
	pairs := make(map[relationPairKey]int64)
	adjacency := make(map[relationFieldKey]map[int64][]int64)
	for _, edge := range edges {
		pair := relationPairKey{from: edge.FromNode, to: edge.ToNode, field: edge.Field}
		if previous := pairs[pair]; previous != 0 {
			report.add(RelationIssue{Kind: RelationDuplicate, EdgeID: edge.ID, NodeID: edge.FromNode,
				TargetID: edge.ToNode, Field: edge.Field, Message: fmt.Sprintf("duplicates edge %d", previous)})
		}
		pairs[pair] = edge.ID
		source, sourceOK := nodeByID[edge.FromNode]
		target, targetOK := nodeByID[edge.ToNode]
		if !sourceOK {
			report.add(RelationIssue{Kind: RelationDanglingSource, EdgeID: edge.ID, NodeID: edge.FromNode,
				TargetID: edge.ToNode, Field: edge.Field, Message: "edge source does not exist"})
			continue
		}
		if !targetOK {
			report.add(RelationIssue{Kind: RelationDanglingTarget, EdgeID: edge.ID, NodeID: source.ID,
				NodeType: source.Type, TargetID: edge.ToNode, Field: edge.Field, Message: "edge target does not exist"})
			continue
		}
		field, ok := s.types.Field(source.Type, edge.Field)
		if !ok || !s.types.IsRefKind(field.Kind) {
			report.add(RelationIssue{Kind: RelationUnknownField, EdgeID: edge.ID, NodeID: source.ID,
				NodeType: source.Type, TargetID: target.ID, Field: edge.Field, Message: "edge field is not declared as a ref"})
			continue
		}
		if target.Type != field.To {
			report.add(RelationIssue{Kind: RelationTargetTypeMismatch, EdgeID: edge.ID, NodeID: source.ID,
				NodeType: source.Type, TargetID: target.ID, TargetType: target.Type, Field: edge.Field,
				Message: fmt.Sprintf("target type is %q, want %q", target.Type, field.To)})
		}
		kind, _ := s.types.Kind(field.Kind)
		single := kind.Class() == types.ClassRef
		undirected := field.Symmetric || field.Equivalence
		if edge.SingleRef != single || edge.Symmetric != undirected {
			report.add(RelationIssue{Kind: RelationMetadataMismatch, EdgeID: edge.ID, NodeID: source.ID,
				NodeType: source.Type, TargetID: target.ID, Field: edge.Field, Message: "stored edge metadata differs from schema"})
		}
		if undirected && edge.FromNode >= edge.ToNode {
			report.add(RelationIssue{Kind: RelationNonCanonical, EdgeID: edge.ID, NodeID: source.ID,
				NodeType: source.Type, TargetID: target.ID, Field: edge.Field, Message: "undirected edge endpoints are not canonical"})
		}
		counts[relationCountKey{nodeID: source.ID, field: field.Name}]++
		if undirected {
			counts[relationCountKey{nodeID: target.ID, field: field.Name}]++
		}
		if field.Required {
			if !undirected && source.ArchivedAt == nil && target.ArchivedAt != nil {
				report.add(RelationIssue{Kind: RelationArchivedRequired, EdgeID: edge.ID, NodeID: source.ID,
					NodeType: source.Type, TargetID: target.ID, TargetType: target.Type, Field: field.Name,
					Message: "required ref points to an archived target"})
			}
			if undirected && source.ArchivedAt == nil && target.ArchivedAt != nil {
				report.add(RelationIssue{Kind: RelationArchivedRequired, EdgeID: edge.ID, NodeID: source.ID,
					NodeType: source.Type, TargetID: target.ID, TargetType: target.Type, Field: field.Name,
					Message: "required ref points to an archived target"})
			}
			if undirected && target.ArchivedAt == nil && source.ArchivedAt != nil {
				report.add(RelationIssue{Kind: RelationArchivedRequired, EdgeID: edge.ID, NodeID: target.ID,
					NodeType: target.Type, TargetID: source.ID, TargetType: source.Type, Field: field.Name,
					Message: "required ref points to an archived target"})
			}
		}
		if field.Transitive || isTreeField(s.types, source.Type, field.Name) {
			key := relationFieldKey{typeName: source.Type, field: field.Name}
			if adjacency[key] == nil {
				adjacency[key] = make(map[int64][]int64)
			}
			adjacency[key][source.ID] = append(adjacency[key][source.ID], target.ID)
		}
	}

	for _, node := range nodes {
		if node.ArchivedAt != nil {
			continue
		}
		td, ok := s.types.Type(node.Type)
		if !ok {
			continue
		}
		for _, field := range td.Fields {
			kind, ok := s.types.Kind(field.Kind)
			if !ok || (kind.Class() != types.ClassRef && kind.Class() != types.ClassRefList) {
				continue
			}
			count := counts[relationCountKey{nodeID: node.ID, field: field.Name}]
			if kind.Class() == types.ClassRef && count > 1 {
				report.add(RelationIssue{Kind: RelationCardinality, NodeID: node.ID, NodeType: node.Type,
					Field: field.Name, Message: fmt.Sprintf("single ref has %d edges", count)})
			}
			if field.Required && count == 0 {
				report.add(RelationIssue{Kind: RelationRequiredMissing, NodeID: node.ID, NodeType: node.Type,
					Field: field.Name, Message: "required ref is missing"})
			}
		}
	}
	for key, graph := range adjacency {
		if nodeID, targetID, ok := relationCycleIn(graph); ok {
			report.add(RelationIssue{Kind: RelationCycle, NodeID: nodeID, NodeType: key.typeName,
				TargetID: targetID, Field: key.field, Message: "transitive relation contains a cycle"})
		}
	}
	slices.SortFunc(report.Issues, func(a, b RelationIssue) int {
		return cmp.Or(
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.NodeID, b.NodeID),
			cmp.Compare(a.EdgeID, b.EdgeID),
		)
	})
	return report, nil
}

type relationCountKey struct {
	nodeID int64
	field  string
}

type relationFieldKey struct {
	typeName string
	field    string
}

type relationPairKey struct {
	from  int64
	to    int64
	field string
}

func (r *RelationReport) add(issue RelationIssue) {
	r.Issues = append(r.Issues, issue)
}

func relationCycleIn(graph map[int64][]int64) (int64, int64, bool) {
	state := make(map[int64]uint8)
	var visit func(int64) (int64, int64, bool)
	visit = func(nodeID int64) (int64, int64, bool) {
		state[nodeID] = 1
		for _, targetID := range graph[nodeID] {
			if state[targetID] == 1 {
				return nodeID, targetID, true
			}
			if state[targetID] == 0 {
				if from, to, found := visit(targetID); found {
					return from, to, true
				}
			}
		}
		state[nodeID] = 2
		return 0, 0, false
	}
	for nodeID := range graph {
		if state[nodeID] != 0 {
			continue
		}
		if from, to, found := visit(nodeID); found {
			return from, to, true
		}
	}
	return 0, 0, false
}
