package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/kran/dba"
	gquery "github.com/kran/gcmv2/query"
)

// NodeQuery 描述"选哪些节点"：类型 + 服务端范围 + 条件 + 排序。
//
// 不含分页、不含展开 —— 分页在 GetNodes 的 limit/offset 上，展开是读完之后的独立一步
// Expand。三件事各只有一处：选什么 / 取哪一段 / 补什么（计数因此不会被分页污染）。
type NodeQuery struct {
	Type  string
	Where gquery.Expr
	Scope QueryScope
	Sort  []gquery.SortField
}

const (
	// DefaultCountLimit 列表页默认统计上限: 精确 count 要扫过全部匹配行,
	// 10 万行量级约 17ms、百万行约 170ms, 而列表页本身只要 ~0.7ms。
	// 上限同时决定"能翻到第几页"（10000/25 = 400 页）, 低于它时 total 精确。
	DefaultCountLimit = 10_000
	// CountExact 传 CountNodes 的 countLimit: 精确统计（小表/后台导出用）。
	CountExact = -1
	// MaxPageSize 单次取行的上限（分页读）。
	MaxPageSize = 10_000
)

// GetNodes 读节点列表。Fields 一律完整（引用 id 已在其中）；limit 0 = 不限。
func (s *Service) GetNodes(ctx context.Context, q NodeQuery, limit, offset int) ([]*Node, error) {
	if limit < 0 || offset < 0 {
		return nil, fmt.Errorf("%w: limit/offset must not be negative", ErrInvalidQuery)
	}
	if limit > MaxPageSize {
		return nil, fmt.Errorf("%w: page size exceeds %d", ErrQueryTooComplex, MaxPageSize)
	}
	db, err := s.buildQuery(ctx, q)
	if err != nil {
		return nil, err
	}
	if limit > 0 {
		db = db.Add("LIMIT #{1} OFFSET #{2}", limit, offset)
	}
	nodes, err := db.FetchList[Node]()
	if err != nil {
		return nil, err
	}
	// 读投影：引用 id 补进 Fields（一次批量, SQL 次数与行数无关）。
	if err := s.hydrateFields(ctx, nodes); err != nil {
		return nil, err
	}
	return nodePtrs(nodes), nil
}

// CountNodes 计数（独立读）。countLimit 0 = DefaultCountLimit, CountExact = 精确。
// 超过上限返回上限值（"至少这么多"）—— 大表列表页不必扫完整个匹配集。
func (s *Service) CountNodes(ctx context.Context, q NodeQuery, countLimit int) (int64, error) {
	limit := countLimit
	if limit == 0 {
		limit = DefaultCountLimit
	}
	where, err := s.buildWhere(ctx, q)
	if err != nil {
		return 0, err
	}
	var total *int64
	if limit > 0 {
		total, err = s.db.WithCtx(ctx).Add(`SELECT COUNT(1) FROM (SELECT nodes.id FROM nodes
			WHERE type = #{1} AND #{2} LIMIT #{3})`,
			q.Type, where, limit+1).FetchOne[int64]()
	} else {
		total, err = s.db.WithCtx(ctx).Add(`SELECT COUNT(1) FROM nodes
			WHERE type = #{1} AND #{2}`,
			q.Type, where).FetchOne[int64]()
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

// normalizePage 分页参数归一（页码从 1 起、每页 1..100；搜索路径仍用）。
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

func (s *Service) buildQuery(ctx context.Context, query NodeQuery) (*dba.SQL, error) {
	where, err := s.buildWhere(ctx, query)
	if err != nil {
		return nil, err
	}
	order, err := s.compileSort(ctx, query.Type, query.Sort)
	if err != nil {
		return nil, err
	}
	db := s.db.WithCtx(ctx).Add(
		`SELECT ${F:*} FROM nodes WHERE type = #{1} AND #{2} ${order}`,
		query.Type, where).Var("order", "ORDER BY "+order)
	return db, nil
}

// buildWhere 服务端范围 AND 用户条件（计数与取行共用同一份过滤）。
func (s *Service) buildWhere(ctx context.Context, query NodeQuery) (dba.Node, error) {
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
		// 能否排序同样来自声明：类型字段看 kind.QueryOps，系统列看 SystemField.Ops。
		// （以前系统列完全没查 Sortable —— 按 fields（JSON 容器）排序是被静默放行的。）
		sortable := false
		if path.hasField {
			sortable = s.types.FieldQueryOps(path.field).Sortable
		} else if sys, ok := s.types.SystemField(path.fieldName); ok {
			sortable = sys.Ops.Sortable
		}
		if !sortable {
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
