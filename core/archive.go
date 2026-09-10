package core

import (
	"context"
	"time"

	"github.com/kran/dba"
)

// ArchiveNode soft-deletes an active Node without removing its Edges.
func (s *Service) ArchiveNode(ctx context.Context, id, revision int64) error {
	return s.setArchived(ctx, id, revision, true)
}

// RestoreNode restores an archived Node without rewriting its Edges.
func (s *Service) RestoreNode(ctx context.Context, id, revision int64) error {
	return s.setArchived(ctx, id, revision, false)
}

func (s *Service) setArchived(ctx context.Context, id, revision int64, archived bool) error {
	if revision <= 0 {
		return ErrRevisionConflict
	}
	return s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		node, err := tx.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
		if err != nil {
			return err
		}
		if node == nil {
			return ErrNotFound
		}
		if (archived && node.ArchivedAt != nil) || (!archived && node.ArchivedAt == nil) {
			return nil
		}
		hook := HookNodeBeforeArchive
		if !archived {
			hook = HookNodeBeforeRestore
		}
		err = s.hooks.Fire(hook, tx, id)
		if err != nil {
			return err
		}
		var archivedAt any
		if archived {
			archivedAt = time.Now()
		}
		result, err := tx.Update("nodes", dba.H{
			"archived_at": archivedAt,
			"updated_at":  time.Now(),
			"revision":    dba.Expr("revision + 1"),
		}, `id = #{1} AND revision = #{2}`, id, revision).Exec()
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrRevisionConflict
		}
		if archived {
			_, err = tx.Delete("sessions", `node_id = #{1}`, id).Exec()
			if err != nil {
				return err
			}
		}
		updated, err := tx.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
		if err != nil {
			return err
		}
		afterHook := HookNodeAfterArchive
		if !archived {
			afterHook = HookNodeAfterRestore
		}
		return s.hooks.Fire(afterHook, tx, updated)
	})
}
