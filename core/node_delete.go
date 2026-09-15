package core

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
)

// IncomingReference 指向"谁在引用这个节点"（删除被拒时告诉调用方）。
type IncomingReference struct {
	EdgeID     int64  `json:"edge_id"`
	SourceID   int64  `json:"source_id"`
	SourceType string `json:"source_type"`
	Field      string `json:"field"`
}

// DeleteRestrictedError 节点被引用而不能删除（内核唯一的删除语义）。
type DeleteRestrictedError struct {
	NodeID     int64               `json:"node_id"`
	References []IncomingReference `json:"references"`
}

func (e *DeleteRestrictedError) Error() string {
	parts := make([]string, 0, len(e.References))
	for _, ref := range e.References {
		parts = append(parts, fmt.Sprintf("%s[%d].%s", ref.SourceType, ref.SourceID, ref.Field))
	}
	return fmt.Sprintf("%v: node %d is referenced by %s",
		ErrDeleteRestricted, e.NodeID, strings.Join(parts, ", "))
}

func (e *DeleteRestrictedError) Unwrap() error { return ErrDeleteRestricted }

// deleteNodeTx 永久删除一个节点。只有一种语义：**被引用就不许删**（restrict）——
// 错误里带上引用方，界面据此告诉用户"先解除哪条引用"。
// 删除成功时顺带清掉它自己发出的边（出边）。
func (s *Service) deleteNodeTx(tx *dba.SQL, id int64) error {
	node, err := tx.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
	if err != nil {
		return err
	}
	if node == nil {
		return ErrNotFound
	}
	if err := s.hooks.Fire(HookNodeBeforeDelete, tx, id); err != nil {
		return err
	}
	references, err := s.incomingReferences(tx, id)
	if err != nil {
		return err
	}
	if len(references) > 0 {
		return &DeleteRestrictedError{NodeID: id, References: references}
	}
	if _, err := tx.Delete("edges", `from_node = #{1} OR to_node = #{1}`, id).Exec(); err != nil {
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

// incomingReferences 列出所有指向 targetID 的引用（对称边取另一端）。
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
		refs = append(refs, IncomingReference{
			EdgeID: edge.ID, SourceID: sourceID, SourceType: source.Type, Field: edge.Field,
		})
	}
	return refs, nil
}
