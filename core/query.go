package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/kran/dba"
	gquery "github.com/kran/gcmv2/query"
)

// ListQuery is the single structured query contract. Type and an explicit
// server-created Scope are required.
type ListQuery struct {
	Type   string
	Where  gquery.Expr
	Scope  QueryScope
	Sort   []gquery.SortField
	Expand []gquery.ExpandPath
	Page   gquery.Page
}

// QueryPage executes a paginated schema-aware query.
func (s *Service) QueryPage(ctx context.Context, query ListQuery) ([]Node, int64, error) {
	query.Page = normalizePage(query.Page)
	db, err := s.buildQuery(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	nodes, total, err := db.FetchPage[Node](query.Page.Number, query.Page.Size)
	if err != nil {
		return nil, 0, err
	}
	if err := s.expandQueryNodes(ctx, nodes, query.Expand); err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}

// Query executes a schema-aware query without a count query.
func (s *Service) Query(ctx context.Context, query ListQuery) ([]Node, error) {
	if query.Page.Size <= 0 {
		return nil, fmt.Errorf("%w: query size must be positive", ErrInvalidQuery)
	}
	db, err := s.buildQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	if query.Page.Size > 0 {
		if query.Page.Size > 10_000 {
			return nil, fmt.Errorf("%w: page size exceeds 10000", ErrQueryTooComplex)
		}
		page := max(query.Page.Number, 1)
		db = db.Add("LIMIT #{1} OFFSET #{2}", query.Page.Size, (page-1)*query.Page.Size)
	}
	nodes, err := db.FetchList[Node]()
	if err != nil {
		return nil, err
	}
	if err := s.expandQueryNodes(ctx, nodes, query.Expand); err != nil {
		return nil, err
	}
	return nodes, nil
}

func normalizePage(page gquery.Page) gquery.Page {
	if page.Number <= 0 {
		page.Number = 1
	}
	if page.Size <= 0 {
		page.Size = 20
	}
	if page.Size > 100 {
		page.Size = 100
	}
	return page
}

func (s *Service) buildQuery(ctx context.Context, query ListQuery) (*dba.SQL, error) {
	if query.Type == "" {
		return nil, fmt.Errorf("%w: type required", ErrInvalidQuery)
	}
	effectiveWhere, err := query.Scope.apply(query.Where)
	if err != nil {
		return nil, err
	}
	where, err := s.compileWhere(ctx, query.Type, effectiveWhere)
	if err != nil {
		return nil, err
	}
	order, err := s.compileSort(ctx, query.Type, query.Sort)
	if err != nil {
		return nil, err
	}
	db := s.db.WithCtx(ctx).Add(
		`SELECT ${F:*} FROM nodes WHERE archived_at IS NULL AND type = #{1} AND #{2} ${order}`,
		query.Type, where).Var("order", "ORDER BY "+order)
	return db, nil
}

func (s *Service) compileSort(ctx context.Context, typeName string, fields []gquery.SortField) (string, error) {
	if len(fields) == 0 {
		return `nodes."updated_at" DESC, nodes."id" DESC`, nil
	}
	if len(fields) > 8 {
		return "", fmt.Errorf("%w: sort exceeds 8 fields", ErrQueryTooComplex)
	}
	compiler := &queryCompiler{service: s, ctx: ctx}
	parts := make([]string, 0, len(fields)+1)
	hasID := false
	seen := map[gquery.Path]bool{}
	for _, sortField := range fields {
		if seen[sortField.Path] {
			return "", fmt.Errorf("%w: duplicate sort field %q", ErrInvalidField, sortField.Path.Field)
		}
		seen[sortField.Path] = true
		path, err := compiler.resolvePath(sortField.Path, typeName, "nodes")
		if err != nil {
			return "", err
		}
		if sortField.Path.Kind != gquery.PathSystem && sortField.Path.Kind != gquery.PathField {
			return "", fmt.Errorf("%w: relation %q cannot be sorted", ErrInvalidField, sortField.Path.Field)
		}
		if path.hasField && !s.types.FieldQueryOps(path.field).Sortable {
			return "", fmt.Errorf("%w: field %q cannot be sorted", ErrInvalidField, sortField.Path.Field)
		}
		direction := "ASC"
		if sortField.Desc {
			direction = "DESC"
		}
		parts = append(parts, path.sql+" "+direction)
		if sortField.Path.Kind == gquery.PathSystem && sortField.Path.Field == "id" {
			hasID = true
		}
	}
	if !hasID {
		parts = append(parts, `nodes."id" DESC`)
	}
	return strings.Join(parts, ", "), nil
}

func (s *Service) expandQueryNodes(ctx context.Context, nodes []Node, paths []gquery.ExpandPath) error {
	if len(paths) == 0 || len(nodes) == 0 {
		return nil
	}
	ids := make([]int64, len(nodes))
	for i := range nodes {
		ids[i] = nodes[i].ID
	}
	expanded, err := s.ExpandMany(ctx, ids, paths...)
	if err != nil {
		return err
	}
	byID := make(map[int64]*Node, len(expanded))
	for _, node := range expanded {
		byID[node.ID] = node
	}
	for i := range nodes {
		if node := byID[nodes[i].ID]; node != nil {
			nodes[i].Expand = node.Expand
		}
	}
	return nil
}
