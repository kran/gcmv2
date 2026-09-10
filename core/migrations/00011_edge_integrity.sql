-- +goose Up
-- Storage metadata lets SQLite enforce ref cardinality and symmetric endpoint
-- uniqueness without encoding application Type names into indexes. Service
-- startup populates these flags from the current Schema before creating the
-- partial unique indexes.
ALTER TABLE edges ADD COLUMN single_ref INTEGER NOT NULL DEFAULT 0;
ALTER TABLE edges ADD COLUMN symmetric INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_edges_from_field_sort ON edges(from_node, field, sort, id);

-- +goose StatementBegin
CREATE TRIGGER edges_symmetric_single_insert
BEFORE INSERT ON edges
WHEN NEW.single_ref = 1 AND NEW.symmetric = 1
BEGIN
  SELECT RAISE(ABORT, 'symmetric single ref cardinality violation')
  WHERE EXISTS (
    SELECT 1 FROM edges e
    WHERE e.field = NEW.field AND e.single_ref = 1 AND e.symmetric = 1
      AND (e.from_node IN (NEW.from_node, NEW.to_node)
        OR e.to_node IN (NEW.from_node, NEW.to_node))
  );
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER edges_symmetric_single_update
BEFORE UPDATE OF from_node, to_node, field, single_ref, symmetric ON edges
WHEN NEW.single_ref = 1 AND NEW.symmetric = 1
BEGIN
  SELECT RAISE(ABORT, 'symmetric single ref cardinality violation')
  WHERE EXISTS (
    SELECT 1 FROM edges e
    WHERE e.id <> OLD.id AND e.field = NEW.field
      AND e.single_ref = 1 AND e.symmetric = 1
      AND (e.from_node IN (NEW.from_node, NEW.to_node)
        OR e.to_node IN (NEW.from_node, NEW.to_node))
  );
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS edges_symmetric_single_update;
DROP TRIGGER IF EXISTS edges_symmetric_single_insert;
DROP INDEX IF EXISTS idx_edges_symmetric_single_to;
DROP INDEX IF EXISTS idx_edges_single_from;
DROP INDEX IF EXISTS idx_edges_from_field_sort;
ALTER TABLE edges DROP COLUMN symmetric;
ALTER TABLE edges DROP COLUMN single_ref;
