package core

import (
	"time"
)

// Edge 引用（edges 表的行 — 类型系统不可见, 用户只见"引用字段"）。
type Edge struct {
	ID        int64     `db:"id,omitempty" json:"id"`
	FromNode  int64     `db:"from_node" json:"from_node"`
	Field     string    `db:"field" json:"field"`
	ToNode    int64     `db:"to_node" json:"to_node"`
	Sort      int       `db:"sort" json:"sort"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// OutEdges 出边（分页）。symmetric 字段: 双向展开（存一条查两向）。
func (s *Service) OutEdges(typeName string, from int64, field string, page, size int) ([]Edge, int64, error) {
	_, sym, err := s.fieldOnType(typeName, field)
	if err != nil {
		return nil, 0, err
	}
	if sym {
		return s.edgePage(`field = #{1} AND (from_node = #{2} OR to_node = #{2})`,
			[]any{field, from}, page, size)
	}
	return s.edgePage(`from_node = #{1} AND field = #{2}`, []any{from, field}, page, size)
}

// InEdges 入边（分页）。
func (s *Service) InEdges(to int64, field string, page, size int) ([]Edge, int64, error) {
	return s.edgePage(`to_node = #{1}`, []any{to}, page, size)
}

func (s *Service) edgePage(where string, args []any, page, size int) ([]Edge, int64, error) {
	db := s.db.Add(`SELECT ${F:*} FROM edges WHERE `+where+` ${order:ORDER BY sort, id}`, args...)
	return db.FetchPage[Edge](page, size)
}
