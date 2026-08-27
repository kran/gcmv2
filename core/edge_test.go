package core

import "testing"

// TestAddRefPublic 公开写入口校验: 字段/类型/目标/重复。
func TestAddRefPublic(t *testing.T) {
	s := newTestService(t)
	pid, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	aid, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "x"}})

	// 正常
	if _, err := s.AddEdge(aid, pid, "authors", 1); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	// 重复（UNIQUE）
	if _, err := s.AddEdge(aid, pid, "authors", 0); err == nil {
		t.Fatal("duplicate edge must fail")
	}
	// from 不存在
	if _, err := s.AddEdge(999, pid, "authors", 0); err == nil {
		t.Fatal("missing from must fail")
	}
	// field 不属于类型
	if _, err := s.AddEdge(aid, pid, "ghost", 0); err == nil {
		t.Fatal("unknown field must fail")
	}
	// 非引用字段
	if _, err := s.AddEdge(aid, pid, "body", 0); err == nil {
		t.Fatal("non-ref field must fail")
	}
	// to 类型不匹配（person 的 articles 要 article, 传 category）
	catID, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "c"}})
	if _, err := s.AddEdge(pid, catID, "articles", 0); err == nil {
		t.Fatal("to type mismatch must fail")
	}
}

// TestOutInRefs 出/入边查询 + 分页。
func TestOutInRefs(t *testing.T) {
	s := newTestService(t)
	p1, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "a"}})
	p2, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "b"}})
	a1, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "1", "authors": []any{p1, p2}}})
	a2, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "2", "authors": []any{p1}}})

	// 出边: a1 有 2 条 authors
	out, total, err := s.OutEdges("article", a1, "authors", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(out) != 2 {
		t.Fatalf("a1 authors: total=%d len=%d", total, len(out))
	}
	// 入边: p1 被 2 篇文章引用（inverse 反向）
	in, total, err := s.InEdges(p1, "authors", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(in) != 2 {
		t.Fatalf("p1 inbound: total=%d len=%d", total, len(in))
	}
	// p2 被 1 篇
	if _, total, _ := s.InEdges(p2, "authors", 1, 10); total != 1 {
		t.Fatalf("p2 inbound: %d", total)
	}
	// 分页: 每页 1 条
	_, total, _ = s.OutEdges("article", a1, "authors", 2, 1)
	if total != 2 {
		t.Fatalf("page total: %d", total)
	}
	// a2 出边 1 条
	if _, total, _ = s.OutEdges("article", a2, "authors", 1, 10); total != 1 {
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
	a1, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "1"}})
	a2, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "2"}})
	// 存一条: a1 → a2
	if _, err := s.AddEdge(a1, a2, "related", 0); err != nil {
		t.Fatal(err)
	}
	// a1 的 OutEdges 双向命中
	out, total, _ := s.OutEdges("article", a1, "related", 1, 10)
	if total != 1 || len(out) != 1 {
		t.Fatalf("a1 out: total=%d len=%d", total, len(out))
	}
	// a2 的 OutEdges 也命中（反向）
	out2, total2, _ := s.OutEdges("article", a2, "related", 1, 10)
	if total2 != 1 || len(out2) != 1 || out2[0].FromNode != a1 {
		t.Fatalf("a2 out: total=%d len=%d from=%d", total2, len(out2), out2[0].FromNode)
	}
	// InEdges 同集合
	if _, total3, _ := s.InEdges(a2, "related", 1, 10); total3 != 1 {
		t.Fatalf("a2 in: %d", total3)
	}
}

// TestRemoveRef 删引用。
func TestRemoveRef(t *testing.T) {
	s := newTestService(t)
	pid, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "a"}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "1", "authors": []any{pid}}})
	in, total, _ := s.InEdges(pid, "authors", 1, 10)
	if total != 1 {
		t.Fatalf("inbound: %d", total)
	}
	if err := s.RemoveEdge(in[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, total, _ = s.InEdges(pid, "authors", 1, 10); total != 0 {
		t.Fatalf("after remove: %d", total)
	}
	if err := s.RemoveEdge(999); err == nil {
		t.Fatal("remove missing must fail")
	}
}

// TestMerge 合并: 出/入引用改指向 + 冲突去重 + from 删除。
func TestMerge(t *testing.T) {
	s := newTestService(t)
	// 出边合并 + 冲突去重（category.parent）
	catA, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "A"}})
	catB, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "B"}})
	cFrom, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "from-cat"}})
	cTo, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "to-cat"}})
	s.AddEdge(cFrom, catA, "parent", 0)
	s.AddEdge(cTo, catA, "parent", 0) // 冲突: 同 field+to_node, 合并后只留 to 的
	s.AddEdge(cFrom, catB, "parent", 0)

	if err := s.Merge(cFrom, cTo); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	_, total, _ := s.OutEdges("category", cTo, "parent", 1, 10)
	if total != 2 {
		t.Fatalf("after merge out: %d, want 2 (A dedup + B repointed)", total)
	}
	if n, _ := s.GetNodeById(cFrom); n != nil {
		t.Fatal("from must be deleted")
	}

	// 入边合并（article.authors → person）
	pA, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "A"}})
	pB, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "B"}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "x", "authors": []any{pA}}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"body": "y", "authors": []any{pB}}})
	if err := s.Merge(pA, pB); err != nil {
		t.Fatal(err)
	}
	if _, total, _ := s.InEdges(pB, "authors", 1, 10); total != 2 {
		t.Fatalf("pB inbound after merge: %d, want 2", total)
	}
}

// TestMergeErrors 合并错误: 自身合并 / 缺失。
func TestMergeErrors(t *testing.T) {
	s := newTestService(t)
	pid, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "a"}})
	if err := s.Merge(pid, pid); err == nil {
		t.Fatal("self merge must fail")
	}
	if err := s.Merge(999, pid); err == nil {
		t.Fatal("missing from must fail")
	}
	if err := s.Merge(pid, 999); err == nil {
		t.Fatal("missing to must fail")
	}
}
