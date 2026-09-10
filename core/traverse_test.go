package core

import (
	"reflect"
	"testing"
)

// 树 + 等价类类型定义。
const traverseTypes = `
types:
  category:
    capabilities:
      addressable: { field: slug, unique: global }
      publication: { field: publication_state, draft: draft, published: published }
      tree: { parent: parent, order: position }
    fields:
      - { name: name, kind: textarea }
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: position, kind: number, default: 0 }
      - { name: parent, kind: ref, to: category, transitive: true }
      - { name: children, kind: "ref[]", to: category }
      - { name: synonym, kind: "ref[]", to: category, equivalence: true }
`

func newTraverseService(t *testing.T) *Service {
	t.Helper()
	return New(testDB(t), newTypes(t, traverseTypes))
}

// 造树: root → a → b（b.parent=a, a.parent=root）
func buildTree(t *testing.T, s *Service) (root, a, b int64) {
	t.Helper()
	root, _ = s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "root"}})
	a, _ = s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "a", "parent": root}})
	b, _ = s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "b", "parent": a}})
	return
}

func TestTraverseUp(t *testing.T) {
	s := newTraverseService(t)
	root, a, b := buildTree(t, s)

	// b 向上: [a, root]
	got, err := s.Traverse(t.Context(), "category", b, "parent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{root, a}) { // ORDER BY id
		t.Fatalf("traverse b: %v", got)
	}
	// a 向上: [root]
	got, _ = s.Traverse(t.Context(), "category", a, "parent", 10)
	if !reflect.DeepEqual(got, []int64{root}) {
		t.Fatalf("traverse a: %v", got)
	}
	// root 向上: 空
	got, _ = s.Traverse(t.Context(), "category", root, "parent", 10)
	if len(got) != 0 {
		t.Fatalf("traverse root: %v", got)
	}
}

func TestSubtreeDown(t *testing.T) {
	s := newTraverseService(t)
	root, a, b := buildTree(t, s)

	got, err := s.Subtree(t.Context(), "category", root, "parent", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{a, b}) {
		t.Fatalf("subtree root: %v", got)
	}
	// a 的子树: [b]
	got, _ = s.Subtree(t.Context(), "category", a, "parent", 10)
	if !reflect.DeepEqual(got, []int64{b}) {
		t.Fatalf("subtree a: %v", got)
	}
	// b 无子树
	got, _ = s.Subtree(t.Context(), "category", b, "parent", 10)
	if len(got) != 0 {
		t.Fatalf("subtree b: %v", got)
	}
}

func TestTraverseMaxHops(t *testing.T) {
	s := newTraverseService(t)
	_, a, b := buildTree(t, s)

	// 1 跳: 只到 a
	got, _ := s.Traverse(t.Context(), "category", b, "parent", 1)
	if !reflect.DeepEqual(got, []int64{a}) {
		t.Fatalf("1 hop: %v", got)
	}
	// 子树 1 跳: root 只到 a
	got, _ = s.Subtree(t.Context(), "category", a, "parent", 1)
	if !reflect.DeepEqual(got, []int64{b}) {
		t.Fatalf("subtree 1 hop: %v", got)
	}
}

func TestEquivalenceClass(t *testing.T) {
	s := newTraverseService(t)
	// 等价类: x ↔ y ↔ z（只存单向边 x→y, y→z — 等价无方向, 类内全可达）
	x, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "x"}})
	y, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "y"}})
	z, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "z"}})
	alone, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "alone"}})
	s.AddEdge(t.Context(), x, y, "synonym", 0)
	s.AddEdge(t.Context(), y, z, "synonym", 0)

	got, err := s.EquivalenceClass(t.Context(), "category", y, "synonym", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{x, y, z}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("class y: %v, want %v", got, want)
	}
	if edges, total, err := s.OutEdges(t.Context(), "category", z, "synonym", 1, 10); err != nil || total != 1 || len(edges) != 1 {
		t.Fatalf("equivalence OutEdges reverse = %d, %#v, %v", total, edges, err)
	}
	// 孤立节点: 只有自己
	got, _ = s.EquivalenceClass(t.Context(), "category", alone, "synonym", 10)
	if !reflect.DeepEqual(got, []int64{alone}) {
		t.Fatalf("class alone: %v", got)
	}
}

func TestTransitiveRelationRejectsCycle(t *testing.T) {
	s := newTraverseService(t)
	a, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "a"}})
	b, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "b"}})
	if _, err := s.AddEdge(t.Context(), a, b, "parent", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(t.Context(), b, a, "parent", 0); err == nil {
		t.Fatal("cycle must be rejected")
	}
	got, err := s.Traverse(t.Context(), "category", a, "parent", 5)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{b}) {
		t.Fatalf("traverse after rejected cycle: %v", got)
	}
	if _, err := s.EquivalenceClass(t.Context(), "category", a, "parent", 10); err == nil {
		t.Fatal("non-equivalence field must be rejected")
	}
}

// 非引用字段/未知字段遍历 fail-loud。
func TestTraverseValidation(t *testing.T) {
	s := newTraverseService(t)
	root, _, _ := buildTree(t, s)
	if _, err := s.Traverse(t.Context(), "category", root, "ghost", 10); err == nil {
		t.Fatal("unknown field must fail")
	}
	if _, err := s.Traverse(t.Context(), "category", root, "name", 10); err == nil {
		t.Fatal("non-ref field must fail")
	}
	if _, err := s.Subtree(t.Context(), "category", 999, "parent", 10); err == nil {
		t.Fatal("missing start must fail")
	}
	if _, err := s.Traverse(t.Context(), "category", root, "children", 10); err == nil {
		t.Fatal("non-transitive ref must fail")
	}
}

// Ancestors: 深度序（根→叶）— Traverse 是 id 序, 链场景必须层级序。
func TestAncestorsDepthOrder(t *testing.T) {
	s := newFilterSvc(t)
	// 先建叶后建根 — id 序与层级序相反: leaf(id1) → mid(id2) → top(id3)
	// 父链: leaf 的父 = mid, mid 的父 = top
	top, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "顶", "slug": "top"}})
	mid, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "中", "slug": "mid", "parent": top}})
	leaf, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "叶", "parent": mid}})
	// Traverse(id 序) 与 Ancestors(深度序) 对照
	tr, err := s.Traverse(t.Context(), "category", leaf, "parent", 20)
	if err != nil {
		t.Fatal(err)
	}
	anc, err := s.Ancestors(t.Context(), "category", leaf, "parent", 20)
	if err != nil {
		t.Fatal(err)
	}
	// id 序: top(id1) < mid(id2) → Traverse = [top, mid]（恰好正序, 这里不断言）
	// 深度序: 根→叶 = [top, mid]
	if len(anc) != 2 || anc[0].Fields.Str("slug") != "top" || anc[1].Fields.Str("slug") != "mid" {
		t.Fatalf("Ancestors 根→叶: %v (traverse=%v)", anc, tr)
	}
}
