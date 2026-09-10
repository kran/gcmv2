package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
)

// IncomingReference describes one Node that refers to a deletion target.
type IncomingReference struct {
	EdgeID     int64          `json:"edge_id"`
	SourceID   int64          `json:"source_id"`
	SourceType string         `json:"source_type"`
	Field      string         `json:"field"`
	OnDelete   types.OnDelete `json:"on_delete"`
}

// DeleteRestrictedError reports the references blocking permanent deletion.
type DeleteRestrictedError struct {
	NodeID     int64               `json:"node_id"`
	References []IncomingReference `json:"references"`
}

func (e *DeleteRestrictedError) Error() string {
	parts := make([]string, 0, len(e.References))
	for _, ref := range e.References {
		parts = append(parts, fmt.Sprintf("%s[%d].%s", ref.SourceType, ref.SourceID, ref.Field))
	}
	return fmt.Sprintf("%v: node %d is referenced by %s", ErrDeleteRestricted, e.NodeID, strings.Join(parts, ", "))
}

func (e *DeleteRestrictedError) Unwrap() error { return ErrDeleteRestricted }

func (s *Service) deleteNodeTx(tx *dba.SQL, id int64, deleting map[int64]bool) error {
	if deleting[id] {
		return nil
	}
	node, err := tx.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
	if err != nil {
		return err
	}
	if node == nil {
		return ErrNotFound
	}
	deleting[id] = true

	err = s.hooks.Fire(HookNodeBeforeDelete, tx, id)
	if err != nil {
		return err
	}
	references, err := s.incomingReferences(tx, id)
	if err != nil {
		return err
	}
	cascadeSources := make(map[int64]bool)
	setNullSources := make(map[int64]bool)
	for _, ref := range references {
		switch ref.OnDelete {
		case types.OnDeleteCascade:
			cascadeSources[ref.SourceID] = true
		case types.OnDeleteSetNull:
			setNullSources[ref.SourceID] = true
		}
	}
	blocked := make([]IncomingReference, 0)
	for _, ref := range references {
		if ref.OnDelete == types.OnDeleteRestrict && !cascadeSources[ref.SourceID] && !deleting[ref.SourceID] {
			blocked = append(blocked, ref)
		}
	}
	if len(blocked) > 0 {
		return &DeleteRestrictedError{NodeID: id, References: blocked}
	}
	for sourceID := range cascadeSources {
		if deleting[sourceID] {
			continue
		}
		err = s.deleteNodeTx(tx, sourceID, deleting)
		if err != nil {
			return err
		}
	}
	for sourceID := range setNullSources {
		if cascadeSources[sourceID] || deleting[sourceID] {
			continue
		}
		_, err = tx.Update("nodes", dba.H{
			"revision":   dba.Expr("revision + 1"),
			"updated_at": time.Now(),
		}, `id = #{1}`, sourceID).Exec()
		if err != nil {
			return err
		}
	}
	_, err = tx.Delete("edges", `from_node = #{1} OR to_node = #{1}`, id).Exec()
	if err != nil {
		return err
	}
	result, err := tx.Delete("nodes", `id = #{1}`, id).Exec()
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return s.hooks.Fire(HookNodeAfterDelete, tx, id)
}

func (s *Service) incomingReferences(tx *dba.SQL, targetID int64) ([]IncomingReference, error) {
	edges, err := tx.Add(`SELECT * FROM edges
		WHERE to_node = #{1} OR (symmetric = 1 AND from_node = #{1})
		ORDER BY id`, targetID).FetchList[Edge]()
	if err != nil {
		return nil, err
	}
	refs := make([]IncomingReference, 0, len(edges))
	for _, edge := range edges {
		sourceID := edge.FromNode
		if edge.Symmetric && edge.FromNode == targetID {
			sourceID = edge.ToNode
		}
		if sourceID == targetID {
			continue
		}
		source, err := tx.Select("nodes", `id = #{1}`, sourceID).FetchOne[Node]()
		if err != nil {
			return nil, err
		}
		if source == nil {
			return nil, fmt.Errorf("core: edge %d source %d not found", edge.ID, sourceID)
		}
		field, ok := s.types.Field(source.Type, edge.Field)
		if !ok || !s.types.IsRefKind(field.Kind) {
			return nil, fmt.Errorf("core: edge %d field %s.%s is not defined as ref", edge.ID, source.Type, edge.Field)
		}
		refs = append(refs, IncomingReference{
			EdgeID: edge.ID, SourceID: sourceID, SourceType: source.Type,
			Field: edge.Field, OnDelete: field.OnDelete,
		})
	}
	return refs, nil
}
