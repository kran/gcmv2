package core

import (
	"fmt"
	"strings"

	"github.com/kran/gcmv2/types"
)

// nodesByIDs 批量取节点（IN 一次查询）。
func (s *Service) nodesByIDs(ids []int64) ([]Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// #{1|expand}: dba 展开切片为独立占位符（不再手工拼占位符）
	q := s.db.Add(`SELECT * FROM nodes WHERE id IN (#{1|expand})`, ids)
	rows, err := q.FetchList[Node]()
	if err != nil {
		return nil, fmt.Errorf("core: nodesByIDs: %w", err)
	}
	return rows, nil
}

// ── ExpandPath: 表达式驱动的路径展开 ───────────────
//
// 表达式语法（PocketBase 风格, 符号前缀可改）:
//
//	"authors, <-categories, employment.org"
//
//	逗号 = 并行字段（各自展开, 挂根节点 Expand）
//	点号 = 串行路径（前段展开的节点继续展开后段）
//	"<-" 前缀 = 入边（谁引用我）; 无标记 = 出边（默认）
//
// 结果形态: 出边由 Class 驱动（ClassRef → *Node 单值, ClassRefList → []*Node）;
// 入边永远是 []*Node（"谁引用我"无唯一约束, 来源是集合）。
// 路径长度显式（无 depth 数字）→ 天然有界; 环由显式路径规避。
// 实现: 单节点 ExpandPath 委托批量版（单元素列表）— 一套批量逻辑,
// 查询次数 = 路径长度, 与节点数无关。

// ExpandPath 按表达式展开单个节点（委托批量版, 单元素列表）。
func (s *Service) ExpandPath(id int64, expr string) (*Node, error) {
	nodes, err := s.ExpandPathMany([]int64{id}, expr)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, ErrNotFound
	}
	return nodes[0], nil
}

// parseExpandExpr 解析表达式: "a, b.c, <-d" → 路径段序列。
// 外层逗号并行 + 统一路径语言（types.ParsePath）; 段语义 = 引用字段名,
// JSON 段（$.x）在展开语境无意义 → 拒绝（fail-loud）。
func parseExpandExpr(expr string) ([][]types.Seg, error) {
	var out [][]types.Seg
	for _, raw := range strings.Split(expr, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		path, err := types.ParsePath(raw)
		if err != nil {
			return nil, fmt.Errorf("core: expand: %w", err)
		}
		if len(path) > 4 {
			return nil, fmt.Errorf("core: expand: path %q too deep (%d segments, max 4) — nested expansion grows exponentially", raw, len(path))
		}
		for _, seg := range path {
			if seg.JSON {
				return nil, fmt.Errorf("core: expand: $. not allowed in %q (expand walks ref fields, not JSON)", raw)
			}
		}
		out = append(out, path)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("core: expand: empty expression")
	}
	return out, nil
}

// expandKey 展开容器 key: 与表达式 token 原样一致 — 出边 "field",
// 入边 "<-field"（同名字段双向展开不冲突, 模板 index 访问）。
func expandKey(seg types.Seg) string {
	if seg.In {
		return "<-" + seg.Field
	}
	return seg.Field
}

// ── 批量 expand（列表场景, 避免 N+1）───────────────
//
// ExpandPathMany: 一次调用展开整个节点列表的引用。查询次数 = 路径长度
// （每段一次批量边查询 + 一次批量节点查询）, 与列表大小无关。
// 单节点 ExpandPath 委托本实现（单元素列表）。

// ExpandPathMany 批量路径展开: 返回根节点列表（每个带 Expand 容器）。
// 字段校验用全局定义（列表场景类型一致; 出/入边都允许）。
// expr 支持 "*"（或空）: 按首节点类型展开全部出边 ref 字段（一层, 引擎语义,
// admin 列表回显/编辑预置共用 — 不再由消费方拼字段）。
func (s *Service) ExpandPathMany(ids []int64, expr string) ([]*Node, error) {
	nodes, err := s.nodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*Node, 0, len(nodes))
	for i := range nodes {
		ptrs = append(ptrs, &nodes[i])
	}
	expr = strings.TrimSpace(expr)
	if expr == "" || expr == "*" {
		if len(ptrs) == 0 {
			return ptrs, nil
		}
		expr = s.autoExpandExpr(ptrs[0].Type)
	}
	if expr == "" {
		return ptrs, nil // 类型无 ref 字段: 原样返回
	}
	paths, err := parseExpandExpr(expr)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if err := s.expandBatch(ptrs, path, 0); err != nil {
			return nil, err
		}
	}
	return ptrs, nil
}

// autoExpandExpr 该类型全部出边 ref 字段的逗号表达式（"authors, categories"）;
// 无 ref 字段 → 空串。引擎级"*"语义（admin/render 共用）。
func (s *Service) autoExpandExpr(typeName string) string {
	td, ok := s.types.Type(typeName)
	if !ok {
		return ""
	}
	fields := []string{}
	for _, f := range td.Fields {
		if s.types.IsRefKind(f.Kind) {
			fields = append(fields, f.Name)
		}
	}
	return strings.Join(fields, ", ")
}

// expandBatch 在 nodes 批量展开 path[segIdx:]: 一次批量查边 + 批量取节点。
func (s *Service) expandBatch(nodes []*Node, path []types.Seg, segIdx int) error {
	seg := path[segIdx]
	// 形态信息（纯内存读表, 不校验不报错）: 字段不存在于任何类型 = 数组容器空展开
	var single bool
	for _, typeName := range s.types.Names() {
		if f, ok := s.types.Field(typeName, seg.Field); ok && s.types.IsRefKind(f.Kind) {
			if kind, kOK := s.types.Kind(f.Kind); kOK {
				single = kind.Class() == types.ClassRef
			}
			break
		}
	}
	single = single && !seg.In // 入边永远数组
	// 1. 批量查边（该层所有节点的出/入边, 一次查询）
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	edgesByNode, err := s.edgesFor(ids, seg.Field, seg.In, 1000)
	if err != nil {
		return err
	}
	// 2. 目标节点批量取（去重）
	targetIDs := []int64{}
	seen := map[int64]bool{}
	for _, edges := range edgesByNode {
		for _, e := range edges {
			id := e.ToNode
			if seg.In {
				id = e.FromNode
			}
			if !seen[id] {
				seen[id] = true
				targetIDs = append(targetIDs, id)
			}
		}
	}
	targets, err := s.nodesByIDs(targetIDs)
	if err != nil {
		return err
	}
	byID := map[int64]*Node{}
	for i := range targets {
		byID[targets[i].ID] = &targets[i]
	}
	// 3. 挂 Expand（形态: 出边段 ClassRef 单值 / 其余数组 — 入边永远数组）
	for _, n := range nodes {
		if n.Expand == nil {
			n.Expand = map[string]any{}
		}
		edges := edgesByNode[n.ID]
		ptrs := make([]*Node, 0, len(edges))
		for _, e := range edges {
			id := e.ToNode
			if seg.In {
				id = e.FromNode
			}
			if t := byID[id]; t != nil {
				ptrs = append(ptrs, t)
			}
		}
		if single {
			if len(ptrs) > 0 {
				n.Expand[expandKey(seg)] = ptrs[0]
			}
		} else {
			n.Expand[expandKey(seg)] = ptrs
		}
	}
	// 4. 递归下一段（下一层节点们 = 所有目标）
	if segIdx+1 < len(path) {
		next := make([]*Node, 0, len(targets))
		for _, t := range targets {
			next = append(next, byID[t.ID])
		}
		if err := s.expandBatch(next, path, segIdx+1); err != nil {
			return err
		}
	}
	return nil
}

// expandRefLimit 单字段展开上限（爆炸防护: 超限响亮报错）。
const expandRefLimit = 1000

// edgesFor 批量查边: ids 集合的出边（out=true 按 from_node）或入边（按 to_node）。
// 返回 map[节点id][]Edge — 一次查询, 按节点分组。

func (s *Service) edgesFor(ids []int64, field string, in bool, max int) (map[int64][]Edge, error) {
	if len(ids) == 0 {
		return map[int64][]Edge{}, nil
	}
	col := "from_node"
	if in {
		col = "to_node"
	}
	// LIMIT max+1 探测: 取到 max+1 行 = 超限（不静默截断）
	var rows []Edge
	q := s.db.Add(
		`SELECT * FROM edges WHERE field = #{1} AND #{2|quote} IN (#{3|expand})
		 ORDER BY #{2|quote}, sort, id LIMIT #{4}`,
		field, col, ids, max+1)
	rows, err := q.FetchList[Edge]()
	if err != nil {
		return nil, fmt.Errorf("core: edgesFor: %w", err)
	}
	if len(rows) > max {
		return nil, fmt.Errorf("core: expand %q: refs exceed %d limit (expand is for assembly; use inRefs/outRefs pagination for large sets)", field, max)
	}
	out := map[int64][]Edge{}
	for _, e := range rows {
		k := e.FromNode
		if in {
			k = e.ToNode
		}
		out[k] = append(out[k], e)
	}
	return out, nil
}
