package core

import (
	"path/filepath"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite"
)

const testTypesYAML = `
types:
  category:
    title: name
    search: true
    fields:
      - { name: name, kind: text }
      - { name: parent, kind: ref, to: category }
      - { name: children, kind: "ref[]", to: category }
  person:
    title: name
    search: true
    fields:
      - { name: name, kind: text }
  article:
    title: title
    search: true
    fields:
      - { name: title, kind: text }
      - { name: body, kind: richtext }
      - { name: views, kind: number }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
`

func testDB(t *testing.T) *dba.SQL {
	t.Helper()
	db, err := dba.Open("sqlite", filepath.Join(t.TempDir(), "test.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := migrateUp(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	ts := types.New()
	if err := ts.Load([]byte(testTypesYAML)); err != nil {
		t.Fatal(err)
	}
	return New(testDB(t), ts)
}

// ── Create ─────────────────────────────────────

func TestCreateAndGet(t *testing.T) {
	s := newTestService(t)
	id, err := s.CreateNode(&Node{Type: "article", Display: "t", Slug: "news", Status: 1,
		Fields: map[string]any{"title": "标题", "body": "正文", "views": 5}})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("id = 0")
	}
	n, err := s.GetNodeById(id)
	if err != nil || n == nil {
		t.Fatal(err)
	}
	if n.Display != "t" || n.Slug != "news" || n.Status != 1 {
		t.Fatalf("node = %+v", n)
	}
	if n.Fields["body"] != "正文" || n.Fields["views"] != float64(5) {
		t.Fatalf("fields = %v", n.Fields)
	}
	if n.CreatedAt.IsZero() {
		t.Fatal("created_at empty")
	}
}

func TestCreateSlugDup(t *testing.T) {
	s := newTestService(t)
	if _, err := s.CreateNode(&Node{Type: "article", Display: "t", Slug: "a", Fields: map[string]any{"title": "t1"}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateNode(&Node{Type: "article", Display: "t", Slug: "a", Fields: map[string]any{"title": "t2"}})
	if err == nil {
		t.Fatal("slug dup should fail")
	}
}

func TestCreateRefs(t *testing.T) {
	s := newTestService(t)
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: map[string]any{"name": "c"}})
	id, err := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "categories": []any{cat}}})
	if err != nil {
		t.Fatal(err)
	}
	edges, _, _ := s.OutEdges("article", id, "categories", 1, 10)
	if len(edges) != 1 || edges[0].ToNode != cat {
		t.Fatalf("edges = %+v", edges)
	}
	// fields 不含 ref
	n, _ := s.GetNodeById(id)
	if _, ok := n.Fields["categories"]; ok {
		t.Fatal("ref leaked into fields")
	}
}

// ── Patch（差量 + json_patch merge） ─────────────

func TestPatchColumns(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t", Slug: "a", Status: 0, Sort: 3,
		Fields: map[string]any{"title": "t", "body": "b"}})

	// PATCH: 改 slug + status（Sort 未提供 — 保留）
	slug := "b"
	status := 1
	if err := s.PatchNode(id, &NodePatch{Slug: &slug, Status: &status}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Slug != "b" || n.Status != 1 || n.Sort != 3 {
		t.Fatalf("patch cols = %+v", n)
	}
	if n.Fields["body"] != "b" {
		t.Fatal("fields body lost")
	}
}

func TestPatchSlugEmpty(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t", Slug: "a", Fields: map[string]any{"title": "t"}})
	empty := ""
	if err := s.PatchNode(id, &NodePatch{Slug: &empty}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Slug != "" {
		t.Fatalf("slug should be cleared, got %q", n.Slug)
	}
}

func TestPatchFieldsMerge(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "body": "旧", "views": 100}})

	// PATCH fields: 只给 body — views 保留（json_patch merge）
	if err := s.PatchNode(id, &NodePatch{Fields: map[string]any{"body": "新"}}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Fields["body"] != "新" {
		t.Fatalf("body = %v", n.Fields["body"])
	}
	if n.Fields["views"] != float64(100) {
		t.Fatalf("views lost: %v", n.Fields)
	}
}

func TestPatchFieldsNullDelete(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "body": "旧"}})

	// PATCH fields: body = nil → 删除字段（json_patch RFC 7396）
	if err := s.PatchNode(id, &NodePatch{Fields: map[string]any{"body": nil}}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if _, ok := n.Fields["body"]; ok {
		t.Fatalf("body should be deleted: %v", n.Fields)
	}
}

func TestPatchRefNullClear(t *testing.T) {
	s := newTestService(t)
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: map[string]any{"name": "c"}})
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "categories": []any{cat}}})
	// 清空 ref（null = 只删边不加边 — PATCH 语义）
	if err := s.PatchNode(id, &NodePatch{Fields: map[string]any{"categories": nil}}); err != nil {
		t.Fatal(err)
	}
	edges, _, _ := s.OutEdges("article", id, "categories", 1, 10)
	if len(edges) != 0 {
		t.Fatalf("ref should be cleared: %+v", edges)
	}
}

func TestPatchRefReplace(t *testing.T) {
	s := newTestService(t)
	cat1, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: map[string]any{"name": "c1"}})
	cat2, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: map[string]any{"name": "c2"}})
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "categories": []any{cat1}}})
	// 换引用（只动出现的字段 — 差量）
	if err := s.PatchNode(id, &NodePatch{Fields: map[string]any{"categories": []any{cat2}}}); err != nil {
		t.Fatal(err)
	}
	edges, _, _ := s.OutEdges("article", id, "categories", 1, 10)
	if len(edges) != 1 || edges[0].ToNode != cat2 {
		t.Fatalf("edges = %+v", edges)
	}
}

func TestPatchNoOp(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: map[string]any{"title": "t"}})
	// 空 patch（全 nil + fields 空）— 幂等无错
	if err := s.PatchNode(id, &NodePatch{}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Fields["title"] != "t" {
		t.Fatal("fields changed by empty patch")
	}
}

func TestPatchMissingNode(t *testing.T) {
	s := newTestService(t)
	if err := s.PatchNode(999, &NodePatch{}); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── Delete ─────────────────────────────────────

func TestDelete(t *testing.T) {
	s := newTestService(t)
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: map[string]any{"name": "c"}})
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "categories": []any{cat}}})

	if err := s.DeleteNode(id); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n != nil {
		t.Fatal("node still exists")
	}
	// 入边清理
	edges, _, _ := s.InEdges(cat, "categories", 1, 10)
	if len(edges) != 0 {
		t.Fatal("edges not cleaned")
	}
}

// ── Query ──────────────────────────────────────

func TestQueryPage(t *testing.T) {
	s := newTestService(t)
	for i := 0; i < 5; i++ {
		s.CreateNode(&Node{Type: "article", Display: "t", Sort: i,
			Fields: map[string]any{"title": "t" + string(rune('a'+i))}})
	}
	list, total, err := s.QueryPage(ListQuery{Page: 1, Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(list) != 2 {
		t.Fatalf("total=%d list=%d", total, len(list))
	}
}

// newTypes 独立类型容器（自定义类型定义用）。
func newTypes(t *testing.T, yaml string) *types.Types {
	t.Helper()
	ts := types.New()
	if err := ts.Load([]byte(yaml)); err != nil {
		t.Fatal(err)
	}
	return ts
}

// newFilterSvc 兼容别名（v1 测试 — 用默认 testTypesYAML）。
func newFilterSvc(t *testing.T) *Service {
	t.Helper()
	return New(testDB(t), newTypes(t, testTypesYAML))
}

func TestPatchDisplayEmpty(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "x"}})
	empty := ""
	if err := s.PatchNode(id, &NodePatch{Display: &empty}); err == nil {
		t.Fatal("empty display should be rejected")
	}
	// 非空可改
	ok := "新名字"
	if err := s.PatchNode(id, &NodePatch{Display: &ok}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Display != "新名字" {
		t.Fatalf("display = %q", n.Display)
	}
}
