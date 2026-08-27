package core

// ── 图遍历原语（edges 递归 CTE） ────────────────

// Traverse 沿出边（from → to）递归（含起点）。
func (s *Service) Traverse(typeName string, start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT to_node, 1 FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, w.depth + 1 FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// Subtree 沿入边（to → from）递归（子树, 含起点）。
func (s *Service) Subtree(typeName string, start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT from_node, 1 FROM edges WHERE field = #{1} AND to_node = #{2}
			UNION ALL
			SELECT e.from_node, w.depth + 1 FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// Ancestors 祖先链 根→叶（沿入边从 start 向上, 含自身）。
func (s *Service) Ancestors(typeName string, start int64, field string, maxHops int) ([]*Node, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	ids, err := s.walk(`
		WITH RECURSIVE anc(id, depth) AS (
			SELECT to_node, 1 FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, a.depth + 1 FROM edges e JOIN anc a ON e.from_node = a.id
			WHERE e.field = #{1} AND a.depth < #{3}
		)
		SELECT id FROM anc ORDER BY depth DESC`, field, start, maxHops)
	if err != nil {
		return nil, err
	}
	nodes := make([]*Node, 0, len(ids))
	for _, id := range ids {
		n, err := s.GetNodeById(id)
		if err != nil {
			return nil, err
		}
		if n != nil {
			nodes = append(nodes, n)
		}
	}
	return nodes, nil
}

// walk 递归 CTE 执行（返回 id 列表）。
func (s *Service) walk(cte string, args ...any) ([]int64, error) {
	return s.db.Add(cte, args...).FetchList[int64]()
}

// EquivalenceClass 等价类展开: 沿 field 出边+入边双向递归（含起点）。
func (s *Service) EquivalenceClass(typeName string, start int64, field string, maxHops int) ([]int64, error) {
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT #{2}, 0
			UNION
			SELECT e.to_node, w.depth + 1 FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
			UNION
			SELECT e.from_node, w.depth + 1 FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}
