package core

import (
	"errors"
	"sync"
	"testing"
	"time"

	gquery "github.com/kran/gcmv2/query"
)

// TestAddRefPublic 公开写入口校验: 字段/类型/目标/重复。
func TestAddRefPublic(t *testing.T) {
	s := newTestService(t)
	pid, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	aid, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "t", Fields: Fields{"body": "x"}})

	// 正常
	if _, err := s.AddEdge(t.Context(), aid, pid, "authors", 1); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	// 重复（UNIQUE）
	if _, err := s.AddEdge(t.Context(), aid, pid, "authors", 0); err == nil {
		t.Fatal("duplicate edge must fail")
	}
	// from 不存在
	if _, err := s.AddEdge(t.Context(), 999, pid, "authors", 0); err == nil {
		t.Fatal("missing from must fail")
	}
	// field 不属于类型
	if _, err := s.AddEdge(t.Context(), aid, pid, "ghost", 0); err == nil {
		t.Fatal("unknown field must fail")
	}
	// 非引用字段
	if _, err := s.AddEdge(t.Context(), aid, pid, "body", 0); err == nil {
		t.Fatal("non-ref field must fail")
	}
	// to 类型不匹配（person 的 articles 要 article, 传 category）
	catID, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "t", Fields: Fields{"name": "c"}})
	if _, err := s.AddEdge(t.Context(), pid, catID, "articles", 0); err == nil {
		t.Fatal("to type mismatch must fail")
	}
}

// TestOutInRefs 出/入边查询 + 分页。
func TestOutInRefs(t *testing.T) {
	s := newTestService(t)
	p1, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "t", Fields: Fields{"name": "a"}})
	p2, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "t", Fields: Fields{"name": "b"}})
	a1, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "t", Fields: Fields{"body": "1", "authors": []any{p1, p2}}})
	a2, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "t", Fields: Fields{"body": "2", "authors": []any{p1}}})

	// 出边: a1 有 2 条 authors
	out, total, err := s.OutEdges(t.Context(), "article", a1, "authors", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(out) != 2 {
		t.Fatalf("a1 authors: total=%d len=%d", total, len(out))
	}
	// 入边: p1 被 2 篇文章引用（inverse 反向）
	in, total, err := s.InEdges(t.Context(), p1, "authors", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(in) != 2 {
		t.Fatalf("p1 inbound: total=%d len=%d", total, len(in))
	}
	if _, total, err := s.InEdges(t.Context(), p1, "categories", 1, 10); err != nil || total != 0 {
		t.Fatalf("inbound field filter: total=%d err=%v", total, err)
	}
	// p2 被 1 篇
	if _, total, _ := s.InEdges(t.Context(), p2, "authors", 1, 10); total != 1 {
		t.Fatalf("p2 inbound: %d", total)
	}
	// 分页: 每页 1 条
	_, total, _ = s.OutEdges(t.Context(), "article", a1, "authors", 2, 1)
	if total != 2 {
		t.Fatalf("page total: %d", total)
	}
	// a2 出边 1 条
	if _, total, _ = s.OutEdges(t.Context(), "article", a2, "authors", 1, 10); total != 1 {
		t.Fatalf("a2 authors: %d", total)
	}
}

// TestSymmetricRefs 对称字段: 存一条, Out/In 都双向命中。
func TestSymmetricRefs(t *testing.T) {
	// 需要 symmetric 字段定义
	ts := newTypes(t, `
types:
  article:
    fields:
      - { name: body, kind: richtext }
      - { name: related, kind: "ref[]", to: article, symmetric: true }
`)
	s := New(testDB(t), ts)
	a1, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "t", Fields: Fields{"body": "1"}})
	a2, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "t", Fields: Fields{"body": "2"}})
	// 反向写入也规范化为较小 ID → 较大 ID。
	if _, err := s.AddEdge(t.Context(), a2, a1, "related", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(t.Context(), a1, a2, "related", 0); err == nil {
		t.Fatal("reverse duplicate symmetric edge must fail")
	}
	// a1 的 OutEdges 双向命中
	out, total, _ := s.OutEdges(t.Context(), "article", a1, "related", 1, 10)
	if total != 1 || len(out) != 1 {
		t.Fatalf("a1 out: total=%d len=%d", total, len(out))
	}
	// a2 的 OutEdges 也命中（反向）
	out2, total2, _ := s.OutEdges(t.Context(), "article", a2, "related", 1, 10)
	if total2 != 1 || len(out2) != 1 || out2[0].FromNode != a1 || out2[0].ToNode != a2 {
		t.Fatalf("a2 out: total=%d len=%d from=%d", total2, len(out2), out2[0].FromNode)
	}
	// InEdges 对无向关系也恢复双向语义。
	if _, total3, _ := s.InEdges(t.Context(), a2, "related", 1, 10); total3 != 1 {
		t.Fatalf("a2 in: %d", total3)
	}
	if _, total3, _ := s.InEdges(t.Context(), a1, "related", 1, 10); total3 != 1 {
		t.Fatalf("a1 logical in: %d", total3)
	}
	for _, pair := range [][2]int64{{a1, a2}, {a2, a1}} {
		has, err := s.HasRef(t.Context(), pair[0], "related", pair[1])
		if err != nil || !has {
			t.Fatalf("HasRef(%d,%d) = %v, %v", pair[0], pair[1], has, err)
		}
	}
	list := queryAll(t, s, "article", gquery.OneOf(gquery.Ref("related"), a1))
	if len(list) != 1 || list[0].ID != a2 {
		t.Fatalf("symmetric query = %#v", list)
	}
	expanded, err := s.Expand(t.Context(), a2, gquery.Expand(gquery.Ref("related")))
	if err != nil {
		t.Fatal(err)
	}
	refs, ok := expanded.Expand["related"].([]*Node)
	if !ok || len(refs) != 1 || refs[0].ID != a1 {
		t.Fatalf("symmetric expand = %#v", expanded.Expand["related"])
	}
}

// TestRemoveRef 删引用。
func TestRemoveRef(t *testing.T) {
	s := newTestService(t)
	pid, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "t", Fields: Fields{"name": "a"}})
	s.CreateNode(t.Context(), &Node{Type: "article", Display: "t", Fields: Fields{"body": "1", "authors": []any{pid}}})
	in, total, _ := s.InEdges(t.Context(), pid, "authors", 1, 10)
	if total != 1 {
		t.Fatalf("inbound: %d", total)
	}
	if err := s.RemoveEdge(t.Context(), in[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = s.InEdges(t.Context(), pid, "authors", 1, 10); total != 0 {
		t.Fatalf("after remove: %d", total)
	}
	if err := s.RemoveEdge(t.Context(), 999); err == nil {
		t.Fatal("remove missing must fail")
	}
}

func TestPreviewMergeReportsConflictsWithoutMutation(t *testing.T) {
	s := newTestService(t)
	personA, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "A", Fields: Fields{"name": "A"}})
	personB, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "B", Fields: Fields{"name": "B"}})
	s.CreateNode(t.Context(), &Node{Type: "article", Display: "article", Fields: Fields{"body": "x", "authors": []any{personA}}})

	preview, err := s.PreviewMerge(t.Context(), personA, personB)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.RequiresResolution || len(preview.FieldConflicts) == 0 || len(preview.IncomingEdges) != 1 {
		t.Fatalf("preview = %#v", preview)
	}
	if node, _ := s.GetNodeById(t.Context(), personA); node == nil {
		t.Fatal("preview must not mutate source")
	}
}

func TestPreviewMergeErrors(t *testing.T) {
	s := newTestService(t)
	personID, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "a", Fields: Fields{"name": "a"}})
	articleID, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "a", Fields: Fields{"body": "a"}})
	if _, err := s.PreviewMerge(t.Context(), personID, personID); err == nil {
		t.Fatal("self merge preview must fail")
	}
	if _, err := s.PreviewMerge(t.Context(), 999, personID); err == nil {
		t.Fatal("missing source must fail")
	}
	if _, err := s.PreviewMerge(t.Context(), personID, articleID); err == nil {
		t.Fatal("type mismatch must fail")
	}
}

func TestSingleRefCardinalityAndArchivedTarget(t *testing.T) {
	typesYAML := `
types:
  person:
    fields:
      - { name: name, kind: text }
      - { name: partner, kind: ref, to: person, symmetric: true }
  article:
    fields:
      - { name: title, kind: text }
      - { name: editor, kind: ref, to: person }
      - { name: reviewers, kind: "ref[]", to: person }
`
	s := New(testDB(t), newTypes(t, typesYAML))
	first, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "first", Fields: Fields{"name": "first"}})
	second, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "second", Fields: Fields{"name": "second"}})
	third, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "third", Fields: Fields{"name": "third"}})
	article, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "article", Fields: Fields{"title": "article", "editor": first}})
	if _, err := s.AddEdge(t.Context(), article, second, "editor", 0); !errors.Is(err, ErrRelationCardinality) {
		t.Fatalf("single ref error = %v", err)
	}
	_, err := s.CreateNode(t.Context(), &Node{Type: "article", Display: "duplicate", Fields: Fields{
		"title": "duplicate", "reviewers": []any{first, first},
	}})
	if !errors.Is(err, ErrInvalidFields) {
		t.Fatalf("ref[] duplicate error = %v", err)
	}
	if _, err := s.AddEdge(t.Context(), second, third, "partner", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(t.Context(), first, second, "partner", 0); !errors.Is(err, ErrRelationCardinality) {
		t.Fatalf("symmetric single ref error = %v", err)
	}
	firstNode, _ := s.GetNodeById(t.Context(), first)
	if err := s.ArchiveNode(t.Context(), first, firstNode.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(t.Context(), article, first, "reviewers", 0); !errors.Is(err, ErrNodeArchived) {
		t.Fatalf("archived target error = %v", err)
	}
	articleNode, _ := s.GetNodeById(t.Context(), article)
	if err := s.ArchiveNode(t.Context(), article, articleNode.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(t.Context(), article, third, "reviewers", 0); !errors.Is(err, ErrNodeArchived) {
		t.Fatalf("archived source error = %v", err)
	}
}

func TestSingleRefConcurrentWriteKeepsAtMostOneEdge(t *testing.T) {
	typesYAML := `
types:
  person:
    fields:
      - { name: name, kind: text }
  article:
    fields:
      - { name: title, kind: text }
      - { name: editor, kind: ref, to: person }
`
	s := New(testDB(t), newTypes(t, typesYAML))
	first, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "first", Fields: Fields{"name": "first"}})
	second, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "second", Fields: Fields{"name": "second"}})
	article, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "article", Fields: Fields{"title": "article"}})

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, targetID := range []int64{first, second} {
		wg.Go(func() {
			<-start
			_, err := s.db.Insert("edges", map[string]any{
				"from_node": article, "field": "editor", "to_node": targetID,
				"sort": 0, "single_ref": 1, "symmetric": 0, "created_at": time.Now(),
			}).Exec()
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes > 1 {
		t.Fatalf("concurrent successes = %d, want at most 1", successes)
	}
	if successes == 0 {
		if _, err := s.AddEdge(t.Context(), article, first, "editor", 0); err != nil {
			t.Fatalf("retry after SQLite contention: %v", err)
		}
	}
	if _, total, err := s.OutEdges(t.Context(), "article", article, "editor", 1, 10); err != nil || total != 1 {
		t.Fatalf("single ref rows = %d, err=%v", total, err)
	}
}

func TestReferenceReadAPI(t *testing.T) {
	s := newTestService(t)
	personA, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "a", Fields: Fields{"name": "a"}})
	personB, _ := s.CreateNode(t.Context(), &Node{Type: "person", Display: "b", Fields: Fields{"name": "b"}})
	article, _ := s.CreateNode(t.Context(), &Node{Type: "article", Display: "article", Fields: Fields{
		"body": "body", "authors": []any{personA, personB},
	}})
	ids, err := s.RefIDs(t.Context(), article, "authors")
	if err != nil || len(ids) != 2 || ids[0] != personA || ids[1] != personB {
		t.Fatalf("RefIDs = %v, %v", ids, err)
	}
	has, err := s.HasRef(t.Context(), article, "authors", personB)
	if err != nil || !has {
		t.Fatalf("HasRef = %v, %v", has, err)
	}
	full, err := s.FullNode(t.Context(), article)
	if err != nil || len(full.Values.Slice("authors")) != 2 || full.Node.Fields.Has("authors") {
		t.Fatalf("FullNode = %#v, %v", full, err)
	}
	root, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "root", Fields: Fields{"name": "root"}})
	child, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "child", Fields: Fields{"name": "child", "parent": root}})
	parent, found, err := s.RefID(t.Context(), child, "parent")
	if err != nil || !found || parent != root {
		t.Fatalf("RefID = %d, %v, %v", parent, found, err)
	}
	if _, _, err := s.RefID(t.Context(), article, "authors"); err == nil {
		t.Fatal("RefID must reject ref[]")
	}
}
