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
	// CountLimit 限制 total 的统计代价（大表列表页）。
	// 0 = 默认 DefaultCountLimit; CountExact = 精确计数; >0 = 自定义上限。
	// 命中数超过上限时返回上限值 —— 即"至少这么多", 调用方按上限渲染（如"10000+"）。
	CountLimit int
}

const (
	// DefaultCountLimit 列表页默认统计上限: 精确 count 要扫过全部匹配行,
	// 10 万行量级约 17ms、百万行约 170ms, 而列表页本身只要 ~0.7ms。
	// 上限同时决定"能翻到第几页"（10000/25 = 400 页）, 低于它时 total 精确。
	DefaultCountLimit = 10_000
	// CountExact 传 CountLimit: CountExact 时精确统计（小表/后台导出用）。
	CountExact = -1
)

// QueryPage executes a paginated schema-aware query.
// total 是截断计数（见 ListQuery.CountLimit）: 超过上限即返回上限, 不再扫完整个匹配集。
func (s *Service) QueryPage(ctx context.Context, query ListQuery) ([]Node, int64, error) {
	query.Page = normalizePage(query.Page)
	total, err := s.countQuery(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}
	nodes, err := s.Query(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}

// countQuery 截断计数: 只数到上限就停（LIMIT 让扫描提前结束）。
func (s *Service) countQuery(ctx context.Context, query ListQuery) (int64, error) {
	limit := query.CountLimit
	if limit == 0 {
		limit = DefaultCountLimit
	}
	where, err := s.buildWhere(ctx, query)
	if err != nil {
		return 0, err
	}
	var total *int64
	if limit > 0 {
		total, err = s.db.WithCtx(ctx).Add(`SELECT COUNT(1) FROM (SELECT nodes.id FROM nodes
			WHERE archived_at IS NULL AND type = #{1} AND #{2} LIMIT #{3})`,
			query.Type, where, limit+1).FetchOne[int64]()
	} else {
		total, err = s.db.WithCtx(ctx).Add(`SELECT COUNT(1) FROM nodes
			WHERE archived_at IS NULL AND type = #{1} AND #{2}`,
			query.Type, where).FetchOne[int64]()
	}
	if err != nil {
		return 0, err
	}
	if total == nil {
		return 0, nil
	}
	if limit > 0 && *total > int64(limit) {
		return int64(limit), nil
	}
	return *total, nil
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
	where, err := s.buildWhere(ctx, query)
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

// buildWhere 服务端范围 AND 用户条件（计数与取行共用同一份过滤）。
func (s *Service) buildWhere(ctx context.Context, query ListQuery) (dba.Node, error) {
	if query.Type == "" {
		return dba.Node{}, fmt.Errorf("%w: type required", ErrInvalidQuery)
	}
	effectiveWhere, err := query.Scope.apply(query.Where)
	if err != nil {
		return dba.Node{}, err
	}
	return s.compileWhere(ctx, query.Type, effectiveWhere)
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
