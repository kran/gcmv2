package core

import (
	"errors"
	"fmt"
	"slices"
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
      - { name: related, kind: "refs", to: article, symmetric: true }
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
		ids, err := s.RefIDs(t.Context(), pair[0], "related")
		has := slices.Contains(ids, pair[1])
		if err != nil || !has {
			t.Fatalf("HasRef(%d,%d) = %v, %v", pair[0], pair[1], has, err)
		}
	}
	list := queryAll(t, s, "article", gquery.OneOf(gquery.Ref("related"), a1))
	if len(list) != 1 || list[0].ID != a2 {
		t.Fatalf("symmetric query = %#v", list)
	}
	// 展开是读完之后的独立一步：先读节点, 再补引用目标
	roots, err := s.GetNodesByIDs(t.Context(), []int64{a2})
	if err != nil {
		t.Fatal(err)
	}
	expandedList, err := s.Expand(t.Context(), roots, gquery.Expand(gquery.Ref("related")))
	if err != nil {
		t.Fatal(err)
	}
	expanded := expandedList[0]
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
	if node, _ := s.nodeRow(t.Context(), personA); node == nil {
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

func TestSingleRefCardinality(t *testing.T) {
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
      - { name: reviewers, kind: "refs", to: person }
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
		t.Fatalf("refs duplicate error = %v", err)
	}
	if _, err := s.AddEdge(t.Context(), second, third, "partner", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEdge(t.Context(), first, second, "partner", 0); !errors.Is(err, ErrRelationCardinality) {
		t.Fatalf("symmetric single ref error = %v", err)
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
	authorIDs, err := s.RefIDs(t.Context(), article, "authors")
	if err != nil || !slices.Contains(authorIDs, personB) {
		t.Fatalf("RefIDs(authors) = %v, %v", authorIDs, err)
	}
	// 值：引用 id 补全进 Fields；存储：引用仍然只在 edges，不进 nodes.fields 的 JSON。
	raw, err := s.nodeRow(t.Context(), article)
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.GetNode(t.Context(), article)
	if err != nil || len(full.Fields.Slice("authors")) != 2 || raw.Fields.Has("authors") {
		t.Fatalf("FullNode = %#v（原始行 = %#v）, %v", full, raw, err)
	}
	root, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "root", Fields: Fields{"name": "root"}})
	child, _ := s.CreateNode(t.Context(), &Node{Type: "category", Display: "child", Fields: Fields{"name": "child", "parent": root}})
	// 单引用的值走读投影的 Fields（RefIDs 只管多引用字段）
	childFull, err := s.GetNode(t.Context(), child)
	if err != nil || childFull.Fields["parent"] != root {
		t.Fatalf("FullNode(parent) = %#v, %v", childFull, err)
	}
	// RefIDs 只管多引用字段：对别的字段 fail-loud（不猜、不返回空）
	if _, err := s.RefIDs(t.Context(), article, "body"); err == nil {
		t.Fatal("RefIDs 对非 refs 字段必须报错")
	}
}

// TestFullNodeRoundTrip 读投影出来的值必须能原样写回：引用读成 int64 / []int64，
// 标量原样；空的多引用字段也要能回写。写回后引用数/顺序不变（是替换不是叠加）。
func TestFullNodeRoundTrip(t *testing.T) {
	s := newTestService(t)
	mk := func(n *Node) int64 {
		id, err := s.CreateNode(t.Context(), n)
		if err != nil {
			t.Fatalf("创建 %s 失败: %v", n.Display, err)
		}
		return id
	}
	cat := mk(&Node{Type: "category", Display: "c", Fields: Fields{"publication_state": "published"}})
	pa := mk(&Node{Type: "person", Display: "a", Fields: Fields{"name": "a"}})
	pb := mk(&Node{Type: "person", Display: "b", Fields: Fields{"name": "b"}})
	full := mk(&Node{Type: "article", Display: "full", Fields: Fields{
		"body": "body", "categories": []int64{cat}, "authors": []int64{pa, pb},
	}})
	partial := mk(&Node{Type: "article", Display: "partial", Fields: Fields{"body": "b"}})

	for _, id := range []int64{full, partial} {
		before, err := s.GetNode(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		rev := before.Revision
		if err := s.PatchNode(t.Context(), id, &NodePatch{Revision: &rev, Fields: before.Fields}); err != nil {
			t.Fatalf("原样写回失败 %#v: %v", before.Fields, err)
		}
		after, err := s.GetNode(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"body"} {
			if fmt.Sprint(before.Fields[name]) != fmt.Sprint(after.Fields[name]) {
				t.Fatalf("%s 写回后变了: %v → %v", name, before.Fields[name], after.Fields[name])
			}
		}
		// 引用字段（单引用是多引用的一种形态，这里两个都是 refs 字段）
		for _, name := range []string{"authors", "categories"} {
			a1, a2 := before.Fields.Slice(name), after.Fields.Slice(name)
			if len(a1) != len(a2) {
				t.Fatalf("%s 写回后数量变了: %v → %v", name, a1, a2)
			}
			for i := range a1 {
				if a1[i] != a2[i] {
					t.Fatalf("%s 写回后变了: %v → %v", name, a1, a2)
				}
			}
		}
	}
}
