package core

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
)

// SortField 是经过 Schema 校验的排序项。Field 使用节点列名（如 id、created_at）
// 或动态字段名（如 $published_at）；不接受 SQL 表达式。
type SortField struct {
	Field string `json:"field"`
	Desc  bool   `json:"desc"`
}

// ListQuery 结构化查询: 过滤（Lisp 表达式）、排序、展开、分页。
type ListQuery struct {
	Filter string      // Lisp filter（空 = 不过滤）
	Sort   []SortField // 空 = 默认 updated_at DESC, id DESC
	Expand string      // 展开表达式（"authors, categories" — 批量路径展开）
	Page   int
	Size   int
}

// QueryPage 分页查询。
func (s *Service) QueryPage(q ListQuery, params ...map[string]any) ([]Node, int64, error) {
	db, err := s.buildQuery(q, params)
	if err != nil {
		return nil, 0, err
	}
	nodes, total, err := db.FetchPage[Node](q.Page, q.Size)
	if err != nil {
		return nil, 0, err
	}
	if q.Expand != "" {
		ids := make([]int64, len(nodes))
		for i := range nodes {
			ids[i] = nodes[i].ID
		}
		expanded, err := s.ExpandPathMany(ids, q.Expand)
		if err != nil {
			return nil, 0, err
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
	}
	return nodes, total, nil
}

// Query 只查不数（Size 即 LIMIT）。
func (s *Service) Query(q ListQuery, params ...map[string]any) ([]Node, error) {
	db, err := s.buildQuery(q, params)
	if err != nil {
		return nil, err
	}
	if q.Size > 0 {
		if q.Page > 1 {
			db = db.Add("LIMIT #{1} OFFSET #{2}", q.Size, (q.Page-1)*q.Size)
		} else {
			db = db.Add("LIMIT #{1}", q.Size)
		}
	}
	return db.FetchList[Node]()
}

func (s *Service) buildQuery(q ListQuery, params []map[string]any) (*dba.SQL, error) {
	var p map[string]any
	if len(params) > 0 {
		p = params[0]
	}
	db := s.db.Add(`SELECT ${F:*} FROM nodes WHERE archived_at IS NULL AND (${where}) ${order:ORDER BY updated_at DESC, id DESC}`)
	var err error
	if q.Filter != "" {
		db, err = s.CompileLispInto(db, q.Filter, p)
		if err != nil {
			return nil, err
		}
	} else {
		db = db.Var("where", "1 = 1")
	}
	if len(q.Sort) > 0 {
		order, err := s.compileSort(q.Sort)
		if err != nil {
			return nil, err
		}
		db = db.Var("order", "ORDER BY "+order)
	}
	return db, nil
}

func (s *Service) compileSort(fields []SortField) (string, error) {
	parts := make([]string, 0, len(fields))
	for _, sortField := range fields {
		field := strings.TrimSpace(sortField.Field)
		if field == "" {
			return "", fmt.Errorf("core: sort field required")
		}
		var expression string
		if strings.HasPrefix(field, "$") {
			name := strings.TrimPrefix(field, "$")
			if !s.hasField(name) {
				return "", fmt.Errorf("core: sort field %q not defined", field)
			}
			expression = `json_extract(nodes.fields, '$.` + name + `')`
		} else {
			if !types.IsNodeColumn(field) || field == "fields" {
				return "", fmt.Errorf("core: sort column %q not allowed", field)
			}
			expression = `nodes."` + field + `"`
		}
		direction := "ASC"
		if sortField.Desc {
			direction = "DESC"
		}
		parts = append(parts, expression+" "+direction)
	}
	return strings.Join(parts, ", "), nil
}

func (s *Service) hasField(name string) bool {
	for _, typeName := range s.types.Names() {
		if _, ok := s.types.Field(typeName, name); ok {
			return true
		}
	}
	return false
}
