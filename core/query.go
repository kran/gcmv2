package core

import (
	"github.com/kran/dba"
)

// ListQuery 结构化查询: 过滤（Lisp 表达式）、排序、展开、分页。
type ListQuery struct {
	Filter string // Lisp filter（空 = 不过滤）
	Sort   string // 排序（空 = 默认 ORDER BY sort, id DESC）
	Expand string // 展开表达式（"authors, categories" — 批量路径展开）
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
		for i := range nodes {
			nodes[i].Expand = expanded[i].Expand
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

// buildQuery 构建查询（Lisp 编译暂缺 — filter 非空报错, 后续补）。
func (s *Service) buildQuery(q ListQuery, params []map[string]any) (*dba.SQL, error) {
	var p map[string]any
	if len(params) > 0 {
		p = params[0]
	}
	db := s.db.Add(`SELECT ${F:*} FROM nodes WHERE ${where} ${order:ORDER BY sort, id DESC}`)
	var err error
	if q.Filter != "" {
		db, err = s.CompileLispInto(db, q.Filter, p)
		if err != nil {
			return nil, err
		}
	} else {
		db = db.Var("where", "1 = 1")
	}
	if q.Sort != "" {
		db = db.Var("order", "ORDER BY "+q.Sort)
	}
	return db, nil
}
