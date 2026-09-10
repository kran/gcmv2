package core

import (
	"context"
	"fmt"

	gquery "github.com/kran/gcmv2/query"
)

// Tree 类型引用树的内存结构 — LoadTree 一次加载, 树操作（导航/子分类/
// 面包屑/子树收集）全内存, 不再查 DB。
//
// 分类树通常小（几十~几百节点）, 全量加载一次 SELECT 是最便宜的选择;
// 每次请求加载, 无缓存失效问题。
type Tree struct {
	nodes     map[int64]*Node   // id → 节点
	byAddress map[string]*Node  // addressable capability 值 → 节点
	parent    map[int64]int64   // child id → parent id
	children  map[int64][]*Node // parent id → 子列表（tree.order, id 序）
	roots     []*Node           // 顶级（tree.order, id 序）
}

// LoadTree 按类型声明的 tree capability 加载树。可见范围由调用方传入的
// Scope 决定: 公开路由传 PolicyScope, 管理/内部路径传 BypassPolicy。
// Core 不假定树必须可发布 — CRM 的组织/区域树没有公开语义。
//
// 只加载 Scope 内的节点; 父节点不在 Scope 内时该节点成为根。
func (s *Service) LoadTree(ctx context.Context, typeName string, scope QueryScope) (*Tree, error) {
	tree, ok := s.types.Tree(typeName)
	if !ok {
		return nil, fmt.Errorf("core: type %q is not tree-enabled", typeName)
	}
	// id 升序兜底必须显式写: compileSort 在排序字段里没有 id 时会追加 id DESC,
	// 那会让同序节点的先后在升级后反转（树/导航顺序变化）。
	sort := []gquery.SortField{gquery.Asc(gquery.System("id"))}
	if tree.Order != "" {
		sort = []gquery.SortField{gquery.Asc(gquery.Field(tree.Order)), gquery.Asc(gquery.System("id"))}
	}
	db, err := s.buildQuery(ctx, ListQuery{Type: typeName, Scope: scope, Sort: sort})
	if err != nil {
		return nil, err
	}
	nodes, err := db.FetchList[Node]()
	if err != nil {
		return nil, err
	}
	t := &Tree{
		nodes:     make(map[int64]*Node, len(nodes)),
		byAddress: make(map[string]*Node, len(nodes)),
		parent:    make(map[int64]int64, len(nodes)),
		children:  make(map[int64][]*Node),
	}
	for i := range nodes {
		n := &nodes[i]
		t.nodes[n.ID] = n
		if address := s.types.Address(n.Type, n.Fields); address != "" {
			t.byAddress[address] = n
		}
	}
	// parent 边（只取本类型来源的边; 树内节点才入 — 指向树外节点的边跳过）
	edges, err := s.db.WithCtx(ctx).Add(`SELECT e.from_node, e.to_node FROM edges e
		JOIN nodes source ON source.id = e.from_node
		WHERE e.field = #{1} AND source.type = #{2}`, tree.Parent, typeName).FetchList[Edge]()
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		if t.nodes[e.FromNode] != nil && t.nodes[e.ToNode] != nil {
			t.parent[e.FromNode] = e.ToNode
		}
	}
	// nodes 已按 (tree.order, id) 排 — 遍历追加即有序
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

// Get 按 id（int64/int/float64）或 addressable 值取节点；未找到 nil。
func (t *Tree) Get(ref any) *Node {
	switch v := ref.(type) {
	case int64:
		return t.nodes[v]
	case int:
		return t.nodes[int64(v)]
	case float64:
		return t.nodes[int64(v)]
	case string:
		return t.byAddress[v]
	}
	return nil
}

// TreeNode 树节点 — Node 组合 + Children（树关系; Node 值模型不含树字段）。
type TreeNode struct {
	Node
	Children []*TreeNode `json:"children"`
}

// JsonNodes 嵌套树序列化（TreeNode — 完整 node 内联 + children）— API 返回用。
// 只含树内节点；排序沿 LoadTree 的 tree.order,id 序。
func (t *Tree) JsonNodes() []*TreeNode {
	return t.walk(t.roots)
}

func (t *Tree) walk(nodes []*Node) []*TreeNode {
	var out []*TreeNode
	for _, n := range nodes {
		out = append(out, &TreeNode{Node: *n, Children: t.walk(t.children[n.ID])})
	}
	return out
}

// Roots 顶级节点列表（tree.order, id 序）。
func (t *Tree) Roots() []*Node { return t.roots }

// Children 子节点列表（tree.order, id 序；ref 无效或叶子 → nil）。
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
