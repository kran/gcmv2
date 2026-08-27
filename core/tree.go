package core

// Tree 类型引用树的内存结构 — LoadTree 一次加载, 树操作（导航/子分类/
// 面包屑/子树收集）全内存, 不再查 DB。
//
// 分类树通常小（几十~几百节点）, 全量加载一次 SELECT 是最便宜的选择;
// 每次请求加载, 无缓存失效问题。
type Tree struct {
	nodes    map[int64]*Node   // id → 节点
	bySlug   map[string]*Node  // slug → 节点（空 slug 不入）
	parent   map[int64]int64   // child id → parent id
	children map[int64][]*Node // parent id → 子列表（sort, id 序）
	roots    []*Node           // 顶级（sort, id 序）
}

// LoadTree 加载类型的 field 树（field = 类型里 to 自身的 ref 字段, 通常 "parent"）。
// 只含 status=1 节点（前台导航语义）; 指向树外（下架）节点的边跳过。
func (s *Service) LoadTree(typeName, field string) (*Tree, error) {
	nodes, err := s.db.Add(`SELECT * FROM nodes WHERE type = #{1} AND status = 1 ORDER BY sort, id`, typeName).FetchList[Node]()
	if err != nil {
		return nil, err
	}
	t := &Tree{
		nodes:    make(map[int64]*Node, len(nodes)),
		bySlug:   make(map[string]*Node, len(nodes)),
		parent:   make(map[int64]int64, len(nodes)),
		children: make(map[int64][]*Node),
	}
	for i := range nodes {
		n := &nodes[i]
		t.nodes[n.ID] = n
		if n.Slug != "" {
			t.bySlug[n.Slug] = n
		}
	}
	// parent 边（全查 Go 过滤 — 树内节点才入; 指向树外（下架/他类型）节点的边跳过）
	edges, err := s.db.Add(`SELECT from_node, to_node FROM edges WHERE field = #{1}`, field).FetchList[Edge]()
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		if t.nodes[e.FromNode] != nil && t.nodes[e.ToNode] != nil {
			t.parent[e.FromNode] = e.ToNode
		}
	}
	// nodes 已按 (sort, id) 排 — 遍历追加即有序
	for i := range nodes {
		n := &nodes[i]
		if pid, ok := t.parent[n.ID]; ok {
			t.children[pid] = append(t.children[pid], n)
		} else {
			t.roots = append(t.roots, n)
		}
	}
	return t, nil
}

// Get 按 id（int64/int/float64 — 模板数字是 float64）或 slug 取节点; 未找到 nil。
func (t *Tree) Get(ref any) *Node {
	switch v := ref.(type) {
	case int64:
		return t.nodes[v]
	case int:
		return t.nodes[int64(v)]
	case float64:
		return t.nodes[int64(v)]
	case string:
		return t.bySlug[v]
	}
	return nil
}

// Roots 顶级节点列表（sort, id 序）。
func (t *Tree) Roots() []*Node { return t.roots }

// Children 子节点列表（sort, id 序; ref 无效或叶子 → nil）。
func (t *Tree) Children(ref any) []*Node {
	n := t.Get(ref)
	if n == nil {
		return nil
	}
	return t.children[n.ID]
}

// Parent 父节点（顶级或 ref 无效 → nil）。
func (t *Tree) Parent(ref any) *Node {
	n := t.Get(ref)
	if n == nil {
		return nil
	}
	pid, ok := t.parent[n.ID]
	if !ok {
		return nil
	}
	return t.nodes[pid]
}

// Ancestors 祖先链 根→叶（含自身; ref 无效 → nil）。
func (t *Tree) Ancestors(ref any) []*Node {
	n := t.Get(ref)
	if n == nil {
		return nil
	}
	var chain []*Node
	for cur := n; cur != nil; {
		chain = append(chain, cur)
		pid, ok := t.parent[cur.ID]
		if !ok {
			break
		}
		cur = t.nodes[pid]
		if len(chain) > len(t.nodes) { // 环检测: 链长超过节点总数必有环
			break
		}
	}
	// 反转 → 根→叶
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// SubtreeIDs 子树 id 集合（含自身; ref 无效 → nil）。
func (t *Tree) SubtreeIDs(ref any) []int64 {
	n := t.Get(ref)
	if n == nil {
		return nil
	}
	ids := make([]int64, 0, 16)
	var walk func(id int64)
	walk = func(id int64) {
		ids = append(ids, id)
		for _, c := range t.children[id] {
			walk(c.ID)
		}
	}
	walk(n.ID)
	return ids
}

// Subtree 子树节点列表（含自身, DFS 序; ref 无效 → nil）。
func (t *Tree) Subtree(ref any) []*Node {
	n := t.Get(ref)
	if n == nil {
		return nil
	}
	var out []*Node
	var walk func(id int64)
	walk = func(id int64) {
		out = append(out, t.nodes[id])
		for _, c := range t.children[id] {
			walk(c.ID)
		}
	}
	walk(n.ID)
	return out
}

// Len 树内节点总数。
func (t *Tree) Len() int { return len(t.nodes) }
