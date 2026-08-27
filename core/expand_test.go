package core

import (
	"strconv"
	"testing"
)

// ── ExpandPath: 表达式驱动的路径展开 ──────────────

const expandPathTypes = `
types:
  category:
    fields:
      - { name: name, kind: text }
      - { name: broader, kind: ref, to: category }
  person:
    fields:
      - { name: name, kind: text }
  org:
    fields:
      - { name: name, kind: text }
  employment:
    fields:
      - { name: person, kind: ref, to: person }
      - { name: org, kind: ref, to: org }
  article:
    fields:
      - { name: title, kind: text }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
      - { name: employment, kind: "ref[]", to: employment }
`

// 并行多字段 + 自然形态（ref → 单值, ref[] → 数组）。
func TestExpandPathParallel(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	cat, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "行业"}})
	p1, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	p2, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "李四"}})
	art, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{
		"title": "甲", "authors": []any{p1, p2}, "categories": []any{cat}}})

	root, err := s.ExpandPath(art, "authors, categories")
	if err != nil {
		t.Fatal(err)
	}
	// authors: ref[] → 数组
	as, ok := root.Expand["authors"].([]*Node)
	if !ok || len(as) != 2 {
		t.Fatalf("authors: %T %v", root.Expand["authors"], root.Expand["authors"])
	}
	// categories: ref[] → 数组（1 个）
	cs, ok := root.Expand["categories"].([]*Node)
	if !ok || len(cs) != 1 || cs[0].Fields["name"] != "行业" {
		t.Fatalf("categories: %v", cs)
	}
}

// 路径（点号）: categories.broader — 分类再展开 broader（ref → 单值）。
func TestExpandPathChain(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	top, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "顶级"}})
	mid, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "中层", "broader": top}})
	art, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "甲", "categories": []any{mid}}})

	root, err := s.ExpandPath(art, "categories.broader")
	if err != nil {
		t.Fatal(err)
	}
	cs := root.Expand["categories"].([]*Node)
	if len(cs) != 1 {
		t.Fatalf("categories: %d", len(cs))
	}
	// 单值: broader → *Node
	b, ok := cs[0].Expand["broader"].(*Node)
	if !ok || b.Fields["name"] != "顶级" {
		t.Fatalf("broader: %T %v", cs[0].Expand["broader"], cs[0].Expand["broader"])
	}
}

// 入边（<-）: 分类下的文章。
func TestExpandPathIn(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	cat, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "行业"}})
	s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "甲", "categories": []any{cat}}})
	s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "乙", "categories": []any{cat}}})

	root, err := s.ExpandPath(cat, "<-categories")
	if err != nil {
		t.Fatal(err)
	}
	arts := root.Expand["<-categories"].([]*Node)
	if len(arts) != 2 {
		t.Fatalf("articles: %d", len(arts))
	}
}

// 混合: 分类 ← 文章 → 作者（出入混合路径）。
func TestExpandPathMixed(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	cat, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "行业"}})
	p1, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "甲", "categories": []any{cat}, "authors": []any{p1}}})

	root, err := s.ExpandPath(cat, "<-categories.authors")
	if err != nil {
		t.Fatal(err)
	}
	arts := root.Expand["<-categories"].([]*Node)
	if len(arts) != 1 {
		t.Fatalf("articles: %d", len(arts))
	}
	as := arts[0].Expand["authors"].([]*Node)
	if len(as) != 1 || as[0].Fields["name"] != "张三" {
		t.Fatalf("authors: %v", as)
	}
}

// 宽松: 未知字段/非 ref 字段不报错（空展开）; 空/`*` = 自动展开。
func TestExpandPathValidation(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	art, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "甲"}})
	if n, err := s.ExpandPath(art, "ghost"); err != nil {
		t.Fatalf("unknown field must be silent: %v", err)
	} else if v, has := n.Expand["ghost"]; !has || len(v.([]*Node)) != 0 {
		t.Fatalf("unknown field expand must be empty container: %v", n.Expand)
	}
	if _, err := s.ExpandPath(art, "  "); err != nil {
		t.Fatalf("empty expr = auto expand: %v", err)
	}
	if n, err := s.ExpandPath(art, "title"); err != nil {
		t.Fatalf("non-ref field must be silent: %v", err)
	} else if v, has := n.Expand["title"]; !has || len(v.([]*Node)) != 0 {
		t.Fatalf("non-ref field expand must be empty container: %v", n.Expand)
	}
}

// "*" 引擎语义: 按类型展开全部出边 ref 字段（一层）。
func TestExpandPathAuto(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	p1, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	art, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "甲", "authors": []any{p1}}})
	n, err := s.ExpandPath(art, "*")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := n.Expand["authors"]; !ok {
		t.Fatalf("auto expand must include authors: %v", n.Expand)
	}
	if _, ok := n.Expand["categories"]; !ok {
		t.Fatalf("auto expand must include categories (empty ok): %v", n.Expand)
	}
}

// 批量 expand: 列表一次展开（查询次数与列表大小无关）。
func TestExpandPathMany(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	cat, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "行业"}})
	p1, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	p2, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "李四"}})
	a1, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "甲", "categories": []any{cat}, "authors": []any{p1}}})
	a2, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "乙", "categories": []any{cat}, "authors": []any{p2}}})

	// 列表两篇, 一次展开 authors + categories
	list, err := s.ExpandPathMany([]int64{a1, a2}, "authors, categories")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list: %d", len(list))
	}
	for _, n := range list {
		as := n.Expand["authors"].([]*Node)
		if len(as) != 1 {
			t.Fatalf("%s authors: %d", n.Fields["title"], len(as))
		}
		cs := n.Expand["categories"].([]*Node)
		if len(cs) != 1 || cs[0].Fields["name"] != "行业" {
			t.Fatalf("%s categories: %v", n.Fields["title"], cs)
		}
	}
	// 列表展示: fields 标题可用（该测试类型未配 title 列声明）
	if list[0].Fields["title"] != "甲" || list[1].Fields["title"] != "乙" {
		t.Fatalf("titles: %v %v", list[0].Fields["title"], list[1].Fields["title"])
	}
}

// 爆炸防护: 单字段超 1000 引用 → fail-loud（不静默截断）。
func TestExpandOverflowFails(t *testing.T) {
	s := newFilterSvc(t)
	// 1500 个作者 + 1 篇文章挂满（绕过 title 用 categories? — article 有 categories ref[]）
	ids := make([]any, 0, 1500)
	for i := 0; i < 1500; i++ {
		id, err := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "c" + strconv.Itoa(i)}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	aid, err := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "big", "categories": ids}})
	if err != nil {
		t.Fatal(err)
	}
	// 单节点版
	if _, err := s.ExpandPath(aid, "categories"); err == nil {
		t.Fatal("single: 1500 refs must fail (not silently truncate)")
	}
	// 批量版
	_, err = s.ExpandPathMany([]int64{aid}, "categories")
	if err == nil {
		t.Fatal("batch: 1500 refs must fail")
	}
}

// 爆炸防护: 链深超 4 段 → fail-loud。
func TestExpandDeepPathFails(t *testing.T) {
	s := newFilterSvc(t)
	p, _ := s.CreateNode(&Node{Type: "person", Fields: Fields{"name": "张三"}})
	a, _ := s.CreateNode(&Node{Type: "article", Fields: Fields{"title": "x", "authors": []any{p}}})
	_, err := s.ExpandPath(a, "authors.authors.authors.authors.authors")
	if err == nil {
		t.Fatal("5-segment path must fail (max 4)")
	}
}

// 双向同名字段展开: 出边 "categories" 与入边 "<-categories" 各自独立（key 不冲突）。
func TestExpandBidirectionalKey(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "子", "parent": root}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished, Fields: Fields{"title": "a", "categories": []any{child}}})

	// 展开子分类: 出边 categories（无, 叶子）+ 入边 <-categories（文章 a 引用它）
	n, err := s.ExpandPath(child, "categories, <-categories")
	if err != nil {
		t.Fatal(err)
	}
	out, hasOut := n.Expand["categories"]
	in, hasIn := n.Expand["<-categories"]
	if !hasOut || !hasIn {
		t.Fatalf("both directions must exist: %v", n.Expand)
	}
	if len(out.([]*Node)) != 0 {
		t.Fatalf("out should be empty: %v", out)
	}
	inList := in.([]*Node)
	if len(inList) != 1 || inList[0].Type != "article" {
		t.Fatalf("in should have the article: %v", inList)
	}
	// 批量版同验证
	nodes, err := s.ExpandPathMany([]int64{child}, "categories, <-categories")
	if err != nil || len(nodes) != 1 {
		t.Fatal(err)
	}
	if _, ok := nodes[0].Expand["<-categories"]; !ok {
		t.Fatal("batch: in key missing")
	}
}

// 多层展开（三层出边链 / 多层入边 / 批量）: 每层递归挂载, 方向每段独立。
func TestExpandMultiLevel(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "子", "parent": root}})
	grand, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "孙", "parent": child}})
	great, _ := s.CreateNode(&Node{Type: "category", Fields: Fields{"name": "重孙", "parent": grand}})
	art, _ := s.CreateNode(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "a", "categories": []any{grand}}})

	// 三层出边链: 文章 → categories(grand) → parent(child) → parent(root)
	n, err := s.ExpandPath(art, "categories.parent.parent")
	if err != nil {
		t.Fatal(err)
	}
	l1 := n.Expand["categories"].([]*Node)
	if len(l1) != 1 || l1[0].ID != grand {
		t.Fatalf("level1: %v", l1)
	}
	// parent 是 ref 单值（非 ref[]）→ 形态 *Node
	l2 := l1[0].Expand["parent"].(*Node)
	if l2.ID != child {
		t.Fatalf("level2: %d", l2.ID)
	}
	l3 := l2.Expand["parent"].(*Node)
	if l3.ID != root {
		t.Fatalf("level3: %d", l3.ID)
	}

	// 多层入边: child 的 <-parent = [grand]; grand 的 <-parent = [great]
	m, err := s.ExpandPath(child, "<-parent.<-parent")
	if err != nil {
		t.Fatal(err)
	}
	in1 := m.Expand["<-parent"].([]*Node)
	if len(in1) != 1 || in1[0].ID != grand {
		t.Fatalf("in level1: %v", in1)
	}
	in2 := in1[0].Expand["<-parent"].([]*Node)
	if len(in2) != 1 || in2[0].ID != great {
		t.Fatalf("in level2: %v", in2)
	}

	// 批量三层
	nodes, err := s.ExpandPathMany([]int64{art, child}, "categories.parent")
	if err != nil || len(nodes) != 2 {
		t.Fatal(err)
	}
	for _, x := range nodes {
		if x.Expand["categories"] == nil && x.Expand["<-parent"] == nil {
			t.Fatalf("batch node %d must have expansion", x.ID)
		}
	}
}
