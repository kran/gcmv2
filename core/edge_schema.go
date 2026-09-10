package core

import (
	"fmt"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
)

// SyncRelationSchema reconciles persisted Edge metadata with the loaded Schema.
// Call it after trusted raw-SQL imports or site migrations that insert edges.
func (s *Service) SyncRelationSchema() error {
	return s.syncEdgeMetadata()
}

// syncEdgeMetadata derives storage-only cardinality/algebra flags from the
// loaded Schema, normalizes undirected endpoints, and recreates the partial
// unique indexes that protect single refs under concurrent writes.
func (s *Service) syncEdgeMetadata() error {
	return s.db.Transaction(func(tx *dba.SQL) error {
		_, err := tx.Add(`DROP TRIGGER IF EXISTS edges_symmetric_single_insert`).Exec()
		if err != nil {
			return err
		}
		_, err = tx.Add(`DROP TRIGGER IF EXISTS edges_symmetric_single_update`).Exec()
		if err != nil {
			return err
		}
		_, err = tx.Add(`DROP INDEX IF EXISTS idx_edges_single_from`).Exec()
		if err != nil {
			return err
		}
		_, err = tx.Add(`DROP INDEX IF EXISTS idx_edges_symmetric_single_to`).Exec()
		if err != nil {
			return err
		}
		_, err = tx.Add(`UPDATE edges SET single_ref = 0, symmetric = 0`).Exec()
		if err != nil {
			return err
		}

		for _, typeName := range s.types.Names() {
			td, _ := s.types.Type(typeName)
			for _, field := range td.Fields {
				kind, ok := s.types.Kind(field.Kind)
				if !ok || (kind.Class() != types.ClassRef && kind.Class() != types.ClassRefList) {
					continue
				}
				single := kind.Class() == types.ClassRef
				undirected := field.Symmetric || field.Equivalence
				err = syncFieldEdgeMetadata(tx, typeName, field.Name, single, undirected)
				if err != nil {
					return err
				}
			}
		}

		err = validateStoredSingleRefs(tx)
		if err != nil {
			return err
		}
		_, err = tx.Add(`CREATE UNIQUE INDEX idx_edges_single_from
			ON edges(from_node, field) WHERE single_ref = 1`).Exec()
		if err != nil {
			return fmt.Errorf("core: edge single-ref index: %w", err)
		}
		_, err = tx.Add(`CREATE UNIQUE INDEX idx_edges_symmetric_single_to
			ON edges(to_node, field) WHERE single_ref = 1 AND symmetric = 1`).Exec()
		if err != nil {
			return fmt.Errorf("core: edge symmetric single-ref index: %w", err)
		}
		_, err = tx.Add(`CREATE TRIGGER edges_symmetric_single_insert
			BEFORE INSERT ON edges WHEN NEW.single_ref = 1 AND NEW.symmetric = 1
			BEGIN
			  SELECT RAISE(ABORT, 'symmetric single ref cardinality violation')
			  WHERE EXISTS (SELECT 1 FROM edges e
			    WHERE e.field = NEW.field AND e.single_ref = 1 AND e.symmetric = 1
			      AND (e.from_node IN (NEW.from_node, NEW.to_node)
			        OR e.to_node IN (NEW.from_node, NEW.to_node)));
			END`).Exec()
		if err != nil {
			return fmt.Errorf("core: edge symmetric single-ref insert trigger: %w", err)
		}
		_, err = tx.Add(`CREATE TRIGGER edges_symmetric_single_update
			BEFORE UPDATE OF from_node, to_node, field, single_ref, symmetric ON edges
			WHEN NEW.single_ref = 1 AND NEW.symmetric = 1
			BEGIN
			  SELECT RAISE(ABORT, 'symmetric single ref cardinality violation')
			  WHERE EXISTS (SELECT 1 FROM edges e
			    WHERE e.id <> OLD.id AND e.field = NEW.field
			      AND e.single_ref = 1 AND e.symmetric = 1
			      AND (e.from_node IN (NEW.from_node, NEW.to_node)
			        OR e.to_node IN (NEW.from_node, NEW.to_node)));
			END`).Exec()
		if err != nil {
			return fmt.Errorf("core: edge symmetric single-ref update trigger: %w", err)
		}
		return nil
	})
}

func syncFieldEdgeMetadata(tx *dba.SQL, typeName, field string, single, undirected bool) error {
	rows, err := tx.Add(`SELECT e.* FROM edges e JOIN nodes n ON n.id = e.from_node
		WHERE n.type = #{1} AND e.field = #{2} ORDER BY e.id`, typeName, field).FetchList[Edge]()
	if err != nil {
		return err
	}
	seen := make(map[[2]int64]int64, len(rows))
	for _, edge := range rows {
		from, to := edge.FromNode, edge.ToNode
		if undirected && from > to {
			from, to = to, from
		}
		key := [2]int64{from, to}
		if previous := seen[key]; previous != 0 {
			return fmt.Errorf(
				"core: edge metadata: duplicate undirected %s.%s edge ids %d and %d",
				typeName, field, previous, edge.ID)
		}
		seen[key] = edge.ID
		_, err = tx.Update("edges", dba.H{
			"from_node":  from,
			"to_node":    to,
			"single_ref": boolInt(single),
			"symmetric":  boolInt(undirected),
		}, `id = #{1}`, edge.ID).Exec()
		if err != nil {
			return err
		}
	}
	return nil
}

func validateStoredSingleRefs(tx *dba.SQL) error {
	row, err := tx.Add(`SELECT endpoint, field, COUNT(1) AS n FROM (
		SELECT from_node AS endpoint, field FROM edges WHERE single_ref = 1
		UNION ALL
		SELECT to_node AS endpoint, field FROM edges WHERE single_ref = 1 AND symmetric = 1
	) GROUP BY endpoint, field HAVING n > 1 LIMIT 1`).FetchOne[struct {
		From  int64  `db:"endpoint"`
		Field string `db:"field"`
		Count int64  `db:"n"`
	}]()
	if err != nil {
		return err
	}
	if row != nil {
		return fmt.Errorf("core: edge metadata: node %d single ref %q has %d edges", row.From, row.Field, row.Count)
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
