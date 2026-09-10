package core

import (
	"reflect"
	"testing"

	gquery "github.com/kran/gcmv2/query"
)

// publishedScope 公开树范围（供树测试复用; 生产由 Web PolicyRegistry 提供）。
func publishedScope() QueryScope {
	return PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
}

// 树形态: root → a → b; root → c（c 下架 status=0 不入树）
func buildTreeForTree(t *testing.T, s *Service) (root, a, b int64) {
	t.Helper()
	root, _ = s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "root", "slug": "root", "publication_state": "published", "position": 1}})
	a, _ = s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "a", "slug": "a", "publication_state": "published", "position": 1, "parent": root}})
	b, _ = s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "b", "slug": "b", "publication_state": "published", "position": 2, "parent": a}})
	c, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "c", "slug": "c", "publication_state": "draft", "parent": root}})
	_ = c
	return
}

func TestTreeBasics(t *testing.T) {
	s := newTraverseService(t)
	root, a, b := buildTreeForTree(t, s)

	tr, err := s.LoadTree(t.Context(), "category", publishedScope())
	if err != nil {
		t.Fatal(err)
	}

	// Len: c 下架不入树
	if tr.Len() != 3 {
		t.Fatalf("len = %d, want 3", tr.Len())
	}

	// Roots: 只 root
	roots := tr.Roots()
	if len(roots) != 1 || roots[0].ID != root {
		t.Fatalf("roots = %v, want [root]", roots)
	}

	// Get: id / float64 / slug
	if tr.Get(root) == nil || tr.Get(float64(a)) == nil || tr.Get("b") == nil {
		t.Fatal("get failed")
	}
	if tr.Get("c") != nil { // 下架取不到
		t.Fatal("c should not be in tree")
	}
	if tr.Get("nope") != nil {
		t.Fatal("unknown slug should be nil")
	}

	// Children: root → [a]; a → [b]; b → nil
	if got := tr.Children(root); len(got) != 1 || got[0].ID != a {
		t.Fatalf("children(root) = %v, want [a]", got)
	}
	if got := tr.Children("a"); len(got) != 1 || got[0].ID != b {
		t.Fatalf("children(a) = %v, want [b]", got)
	}
	if got := tr.Children(b); len(got) != 0 {
		t.Fatalf("children(b) = %v, want empty", got)
	}

	// Parent
	if tr.Parent("b").ID != a {
		t.Fatal("parent(b) != a")
	}
	if tr.Parent(root) != nil {
		t.Fatal("parent(root) should be nil")
	}

	// Ancestors: b → [root, a, b]
	anc := tr.Ancestors(b)
	var ids []int64
	for _, n := range anc {
		ids = append(ids, n.ID)
	}
	if !reflect.DeepEqual(ids, []int64{root, a, b}) {
		t.Fatalf("ancestors(b) = %v, want [root a b]", ids)
	}

	// SubtreeIDs: root → [root, a, b]
	sub := tr.SubtreeIDs(root)
	if !reflect.DeepEqual(sub, []int64{root, a, b}) {
		t.Fatalf("subtreeIDs(root) = %v, want [root a b]", sub)
	}
	// a → [a, b]
	if got := tr.SubtreeIDs(a); !reflect.DeepEqual(got, []int64{a, b}) {
		t.Fatalf("subtreeIDs(a) = %v, want [a b]", got)
	}

	// Subtree 节点列表
	if got := tr.Subtree("a"); len(got) != 2 {
		t.Fatalf("subtree(a) len = %d, want 2", len(got))
	}
}

func TestTreeRejectsCycle(t *testing.T) {
	s := newTraverseService(t)
	a, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "a", "slug": "a", "publication_state": "published"}})
	b, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "b", "slug": "b", "publication_state": "published", "parent": a}})
	if err := patchCurrent(t, s, a, &NodePatch{Fields: Fields{"parent": b}}); err == nil {
		t.Fatal("tree cycle must be rejected")
	}

	tree, err := s.LoadTree(t.Context(), "category", publishedScope())
	if err != nil {
		t.Fatal(err)
	}
	if tree.Parent(b) == nil || tree.Parent(b).ID != a {
		t.Fatal("rejected cycle must leave original tree unchanged")
	}
}

// Tree 不再耦合 publication: 非公开类型（CRM 组织/区域树）用 BypassPolicy 加载,
// 公开路由由 Policy 决定可见范围。
func TestLoadTreeRequiresExplicitScope(t *testing.T) {
	s := New(testDB(t), newTypes(t, `
types:
  department:
    capabilities:
      tree: { parent: parent, order: position }
    fields:
      - { name: name, kind: textarea }
      - { name: position, kind: number, default: 0 }
      - { name: parent, kind: ref, to: department }
`))
	root, _ := s.CreateNode(&Node{Type: "department", Display: "总部", Fields: Fields{"name": "总部"}})
	child, _ := s.CreateNode(&Node{Type: "department", Display: "研发", Fields: Fields{"name": "研发", "parent": root}})

	if _, err := s.LoadTree(t.Context(), "department", QueryScope{}); err == nil {
		t.Fatal("zero scope must be rejected")
	}
	tree, err := s.LoadTree(t.Context(), "department", BypassPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if tree.Len() != 2 || tree.Parent(child) == nil || tree.Parent(child).ID != root {
		t.Fatalf("non-publication tree = %d nodes", tree.Len())
	}
}
