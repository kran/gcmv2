package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// MergeFieldConflict requires an explicit choice before a future merge can be
// executed. Values include scalar fields and reference IDs.
type MergeFieldConflict struct {
	Field  string `json:"field"`
	Source any    `json:"source"`
	Target any    `json:"target"`
}

// MergeCredential identifies a credential that would need to be moved.
type MergeCredential struct {
	ID         int64  `json:"id"`
	Method     string `json:"method"`
	Identifier string `json:"identifier"`
}

// MergePreview is a read-only description of all state affected by a merge.
type MergePreview struct {
	Source             *EditableNode        `json:"source"`
	Target             *EditableNode        `json:"target"`
	SuggestedValues    Fields               `json:"suggested_values"`
	FieldConflicts     []MergeFieldConflict `json:"field_conflicts"`
	IncomingEdges      []Edge               `json:"incoming_edges"`
	OutgoingEdges      []Edge               `json:"outgoing_edges"`
	SourceCredentials  []MergeCredential    `json:"source_credentials"`
	TargetCredentials  []MergeCredential    `json:"target_credentials"`
	RequiresResolution bool                 `json:"requires_resolution"`
}

// PreviewMerge validates two Nodes and reports field, relation, and credential
// effects. The old mutation-only Merge API was removed; execution will require
// an explicit conflict-resolution request and audit support.
func (s *Service) PreviewMerge(ctx context.Context, sourceID, targetID int64) (*MergePreview, error) {
	if sourceID == targetID {
		return nil, errors.New("core: merge preview: source equals target")
	}
	full, err := s.FullNodes(ctx, []int64{sourceID, targetID})
	if err != nil {
		return nil, err
	}
	if len(full) != 2 || full[0].ID != sourceID || full[1].ID != targetID {
		return nil, ErrNotFound
	}
	if full[0].Type != full[1].Type {
		return nil, fmt.Errorf("core: merge preview: type mismatch %q and %q", full[0].Type, full[1].Type)
	}
	preview := &MergePreview{
		Source: full[0], Target: full[1],
		SuggestedValues: make(Fields, len(full[0].Values)+len(full[1].Values)),
		FieldConflicts:  make([]MergeFieldConflict, 0),
	}
	maps.Copy(preview.SuggestedValues, full[1].Values)
	for field, sourceValue := range full[0].Values {
		targetValue, exists := full[1].Values[field]
		if !exists || targetValue == nil {
			preview.SuggestedValues[field] = sourceValue
			continue
		}
		if !reflect.DeepEqual(sourceValue, targetValue) {
			preview.FieldConflicts = append(preview.FieldConflicts, MergeFieldConflict{
				Field: field, Source: sourceValue, Target: targetValue,
			})
		}
	}
	if full[0].Display != full[1].Display {
		preview.FieldConflicts = append(preview.FieldConflicts, MergeFieldConflict{
			Field: "display", Source: full[0].Display, Target: full[1].Display,
		})
	}
	slices.SortFunc(preview.FieldConflicts, func(a, b MergeFieldConflict) int {
		return cmp.Compare(a.Field, b.Field)
	})
	preview.IncomingEdges, err = s.db.WithCtx(ctx).Add(`SELECT * FROM edges
		WHERE to_node = #{1} OR (symmetric = 1 AND from_node = #{1}) ORDER BY id`, sourceID).FetchList[Edge]()
	if err != nil {
		return nil, err
	}
	preview.OutgoingEdges, err = s.db.WithCtx(ctx).Add(`SELECT * FROM edges
		WHERE from_node = #{1} OR (symmetric = 1 AND to_node = #{1}) ORDER BY id`, sourceID).FetchList[Edge]()
	if err != nil {
		return nil, err
	}
	preview.SourceCredentials, err = s.mergeCredentials(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	preview.TargetCredentials, err = s.mergeCredentials(ctx, targetID)
	if err != nil {
		return nil, err
	}
	preview.RequiresResolution = len(preview.FieldConflicts) > 0 ||
		len(preview.IncomingEdges) > 0 || len(preview.OutgoingEdges) > 0 ||
		len(preview.SourceCredentials) > 0
	return preview, nil
}

func (s *Service) mergeCredentials(ctx context.Context, nodeID int64) ([]MergeCredential, error) {
	return s.db.WithCtx(ctx).Add(`SELECT id, method, identifier FROM auth_methods
		WHERE node_id = #{1} ORDER BY id`, nodeID).FetchList[MergeCredential]()
}
