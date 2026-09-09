package core

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite"
)

const testTypesYAML = `
types:
  category:
    capabilities:
      searchable: { fields: [display, name] }
      addressable: { field: slug, unique: global }
      publication: { field: publication_state, draft: draft, published: published }
      tree: { parent: parent, order: position }
    admin: { view: tree, columns: [slug, publication_state, position] }
    fields:
      - { name: name, kind: text }
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: position, kind: number, default: 0 }
      - { name: parent, kind: ref, to: category }
      - { name: children, kind: "ref[]", to: category }
  person:
    capabilities:
      searchable: { fields: [display, name] }
      publication: { field: publication_state, draft: draft, published: published }
    fields:
      - { name: name, kind: text }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
  article:
    capabilities:
      searchable: { fields: [display, title, body] }
      addressable: { field: slug, unique: global }
      publication: { field: publication_state, draft: draft, published: published }
    fields:
      - { name: title, kind: text }
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: position, kind: number, default: 0 }
      - { name: body, kind: richtext }
      - { name: views, kind: number }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
`

func testDB(t testing.TB) *dba.SQL {
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

func patchCurrent(t *testing.T, s *Service, id int64, patch *NodePatch) error {
	t.Helper()
	node, err := s.GetNodeById(id)
	if err != nil {
		return err
	}
	if node == nil {
		return ErrNotFound
	}
	patch.Revision = &node.Revision
	return s.PatchNode(id, patch)
}

func TestNodeSchemaMigration(t *testing.T) {
	db := testDB(t)
	columns, err := db.Add(`SELECT name FROM pragma_table_info('nodes') ORDER BY cid`).FetchList[string]()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "type", "display", "fields", "created_at", "updated_at", "revision", "archived_at"}
	if !slices.Equal(columns, want) {
		t.Fatalf("nodes columns = %v, want %v", columns, want)
	}
	table, err := db.Add(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'legacy_node_columns'`).FetchOne[string]()
	if err != nil || table == nil {
		t.Fatalf("legacy migration table missing: table=%v err=%v", table, err)
	}
}

// ── Create ─────────────────────────────────────

func TestCreateAndGet(t *testing.T) {
	s := newTestService(t)
	id, err := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "标题", "slug": "news", "publication_state": "published", "body": "正文", "views": 5}})
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
	if n.Display != "t" || n.Fields.Str("slug") != "news" || n.Fields.Str("publication_state") != "published" || n.Revision != 1 {
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
	if _, err := s.CreateNode(&Node{Type: "article", Display: "t", Fields: map[string]any{"title": "t1", "slug": "a"}}); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateNode(&Node{Type: "article", Display: "t", Fields: map[string]any{"title": "t2", "slug": "a"}})
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

func TestPatchFieldsAndRevision(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "slug": "a", "publication_state": "draft", "position": 3, "body": "b"}})

	if err := patchCurrent(t, s, id, &NodePatch{Fields: Fields{"slug": "b", "publication_state": "published"}}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Fields.Str("slug") != "b" || n.Fields.Str("publication_state") != "published" || n.Fields.Int("position") != 3 {
		t.Fatalf("patch fields = %+v", n)
	}
	if n.Revision != 2 {
		t.Fatalf("revision = %d, want 2", n.Revision)
	}
}

func TestPatchSlugEmpty(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: map[string]any{"title": "t", "slug": "a"}})
	if err := patchCurrent(t, s, id, &NodePatch{Fields: Fields{"slug": ""}}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Fields.Str("slug") != "" {
		t.Fatalf("slug should be cleared, got %q", n.Fields.Str("slug"))
	}
}

func TestPatchFieldsMerge(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: map[string]any{"title": "t", "body": "旧", "views": 100}})

	// PATCH fields: 只给 body — views 保留（json_patch merge）
	if err := patchCurrent(t, s, id, &NodePatch{Fields: map[string]any{"body": "新"}}); err != nil {
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
	if err := patchCurrent(t, s, id, &NodePatch{Fields: map[string]any{"body": nil}}); err != nil {
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
	if err := patchCurrent(t, s, id, &NodePatch{Fields: map[string]any{"categories": nil}}); err != nil {
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
	if err := patchCurrent(t, s, id, &NodePatch{Fields: map[string]any{"categories": []any{cat2}}}); err != nil {
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

func TestCreateRejectsUnknownField(t *testing.T) {
	s := newTestService(t)
	_, err := s.CreateNode(&Node{
		Type: "article", Display: "t",
		Fields: Fields{"title": "t", "typo": "must fail"},
	})
	if err == nil {
		t.Fatal("unknown create field must fail")
	}
}

func TestPatchValidatesFields(t *testing.T) {
	ts := newTypes(t, `
types:
  item:
    fields:
      - { name: name, kind: text, required: true }
      - { name: state, kind: select, options: [open, closed] }
      - { name: happened_at, kind: timestamp }
`)
	s := New(testDB(t), ts)
	id, err := s.CreateNode(&Node{
		Type: "item", Display: "item",
		Fields: Fields{"name": "item", "state": "open", "happened_at": 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fields := range []Fields{
		{"unknown": "x"},
		{"state": "invalid"},
		{"happened_at": "not-a-number"},
		{"name": nil},
		{"name": ""},
	} {
		if err := patchCurrent(t, s, id, &NodePatch{Fields: fields}); err == nil {
			t.Fatalf("invalid patch must fail: %#v", fields)
		}
	}
	if err := patchCurrent(t, s, id, &NodePatch{Fields: Fields{"state": nil}}); err != nil {
		t.Fatalf("optional field delete: %v", err)
	}
}

func TestCreateDefaultsAndAddress(t *testing.T) {
	s := newTestService(t)
	id, err := s.CreateNode(&Node{
		Type: "article", Display: "defaulted",
		Fields: Fields{"title": "defaulted", "slug": "defaulted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.GetNodeByAddress("defaulted")
	if err != nil || node == nil || node.ID != id {
		t.Fatalf("address lookup: node=%#v err=%v", node, err)
	}
	if node.Fields.Str("publication_state") != "draft" || node.Fields.Int("position") != 0 {
		t.Fatalf("defaults = %#v", node.Fields)
	}
}

func TestPatchRevisionConflict(t *testing.T) {
	s := newTestService(t)
	id, _ := s.CreateNode(&Node{Type: "article", Display: "before", Fields: Fields{"title": "before"}})
	node, _ := s.GetNodeById(id)
	staleRevision := node.Revision
	first := "first"
	if err := s.PatchNode(id, &NodePatch{Revision: &staleRevision, Display: &first}); err != nil {
		t.Fatal(err)
	}
	second := "second"
	err := s.PatchNode(id, &NodePatch{Revision: &staleRevision, Display: &second})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale patch error = %v", err)
	}
}

func TestSchemaUniqueIndex(t *testing.T) {
	ts := newTypes(t, `
types:
  contact:
    constraints:
      unique: [[external_id]]
    fields:
      - { name: external_id, kind: text, required: true }
`)
	s := New(testDB(t), ts)
	_, err := s.CreateNode(&Node{Type: "contact", Display: "one", Fields: Fields{"external_id": "same"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateNode(&Node{Type: "contact", Display: "two", Fields: Fields{"external_id": "same"}})
	if err == nil {
		t.Fatal("duplicate schema unique value must fail")
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
		s.CreateNode(&Node{Type: "article", Display: "t",
			Fields: map[string]any{"title": "t" + string(rune('a'+i)), "views": i, "position": i}})
	}
	list, total, err := s.QueryPage(ListQuery{Page: 1, Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(list) != 2 {
		t.Fatalf("total=%d list=%d", total, len(list))
	}
	list, _, err = s.QueryPage(ListQuery{
		Sort: []SortField{{Field: "$views", Desc: true}}, Page: 1, Size: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Fields.Int("views") != 4 || list[4].Fields.Int("views") != 0 {
		t.Fatalf("dynamic field sort order = %#v", list)
	}
	for _, field := range []string{"id DESC", "id; DELETE FROM nodes", "$missing"} {
		_, _, err := s.QueryPage(ListQuery{Sort: []SortField{{Field: field}}, Page: 1, Size: 5})
		if err == nil {
			t.Fatalf("unsafe sort field %q must fail", field)
		}
	}
}

// newTypes 独立类型容器（自定义类型定义用）。
func newTypes(t testing.TB, yaml string) *types.Types {
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
	if err := patchCurrent(t, s, id, &NodePatch{Display: &empty}); err == nil {
		t.Fatal("empty display should be rejected")
	}
	// 非空可改
	ok := "新名字"
	if err := patchCurrent(t, s, id, &NodePatch{Display: &ok}); err != nil {
		t.Fatal(err)
	}
	n, _ := s.GetNodeById(id)
	if n.Display != "新名字" {
		t.Fatalf("display = %q", n.Display)
	}
}
