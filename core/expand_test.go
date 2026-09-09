package core

import (
	"strconv"
	"strings"
	"testing"
	"time"

	gquery "github.com/kran/gcmv2/query"
)

func expandText(t *testing.T, service *Service, id int64, expression string) (*Node, error) {
	t.Helper()
	paths, err := gquery.ParseExpand(expression)
	if err != nil {
		return nil, err
	}
	return service.Expand(t.Context(), id, paths...)
}

func expandManyText(t *testing.T, service *Service, ids []int64, expression string) ([]*Node, error) {
	t.Helper()
	paths, err := gquery.ParseExpand(expression)
	if err != nil {
		return nil, err
	}
	return service.ExpandMany(t.Context(), ids, paths...)
}

// ── Expand: typed relation paths ─────────────────

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
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "行业"}})
	p1, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	p2, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "李四"}})
	art, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{
		"title": "甲", "authors": []any{p1, p2}, "categories": []any{cat}}})

	root, err := expandText(t, s, art, "authors, categories")
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
	top, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "顶级"}})
	mid, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "中层", "broader": top}})
	art, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "甲", "categories": []any{mid}}})

	root, err := expandText(t, s, art, "categories.broader")
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
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "行业"}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "甲", "categories": []any{cat}}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "乙", "categories": []any{cat}}})

	root, err := expandText(t, s, cat, "<-article.categories")
	if err != nil {
		t.Fatal(err)
	}
	arts := root.Expand["<-article.categories"].([]*Node)
	if len(arts) != 2 {
		t.Fatalf("articles: %d", len(arts))
	}
}

// 混合: 分类 ← 文章 → 作者（出入混合路径）。
func TestExpandPathMixed(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "行业"}})
	p1, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "甲", "categories": []any{cat}, "authors": []any{p1}}})

	root, err := expandText(t, s, cat, "<-article.categories.authors")
	if err != nil {
		t.Fatal(err)
	}
	arts := root.Expand["<-article.categories"].([]*Node)
	if len(arts) != 1 {
		t.Fatalf("articles: %d", len(arts))
	}
	as := arts[0].Expand["authors"].([]*Node)
	if len(as) != 1 || as[0].Fields["name"] != "张三" {
		t.Fatalf("authors: %v", as)
	}
}

func TestExpandPathValidation(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	art, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "甲"}})
	if _, err := expandText(t, s, art, "ghost"); err == nil {
		t.Fatal("unknown relation must fail")
	}
	if _, err := expandText(t, s, art, "  "); err == nil {
		t.Fatal("empty expression must fail")
	}
	if _, err := expandText(t, s, art, "title"); err == nil {
		t.Fatal("non-ref relation must fail")
	}
	if _, err := expandText(t, s, art, "<-categories"); err == nil {
		t.Fatal("incoming relation without source type must fail")
	}
}

// "*" 引擎语义: 按类型展开全部出边 ref 字段（一层）。
func TestExpandPathAuto(t *testing.T) {
	ts := newTypes(t, expandPathTypes)
	s := New(testDB(t), ts)
	p1, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	art, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "甲", "authors": []any{p1}}})
	n, err := s.Expand(t.Context(), art, s.AutoExpand("article")...)
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
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "行业"}})
	p1, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	p2, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "李四"}})
	a1, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "甲", "categories": []any{cat}, "authors": []any{p1}}})
	a2, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "乙", "categories": []any{cat}, "authors": []any{p2}}})

	// 故意按 id 倒序传入，返回顺序必须与请求一致，不能依赖 SQL IN 的返回顺序。
	list, err := expandManyText(t, s, []int64{a2, a1}, "authors, categories")
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
	// 列表展示: fields 标题可用，顺序与输入 ids 一致。
	if list[0].Fields["title"] != "乙" || list[1].Fields["title"] != "甲" {
		t.Fatalf("titles: %v %v", list[0].Fields["title"], list[1].Fields["title"])
	}
}

func TestExpandMixedTypesUsesEachSchema(t *testing.T) {
	ts := newTypes(t, `
types:
  person:
    fields: [{ name: name, kind: text }]
  primary_contact:
    fields: [{ name: people, kind: ref, to: person }]
  contact_group:
    fields: [{ name: people, kind: "ref[]", to: person }]
`)
	s := New(testDB(t), ts)
	person1, _ := s.CreateNode(&Node{Type: "person", Display: "一", Fields: Fields{"name": "一"}})
	person2, _ := s.CreateNode(&Node{Type: "person", Display: "二", Fields: Fields{"name": "二"}})
	primary, _ := s.CreateNode(&Node{Type: "primary_contact", Display: "主联系人", Fields: Fields{"people": person1}})
	group, _ := s.CreateNode(&Node{Type: "contact_group", Display: "联系人组", Fields: Fields{"people": []any{person1, person2}}})

	nodes, err := s.ExpandMany(t.Context(), []int64{primary, group}, gquery.Expand(gquery.Ref("people")))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := nodes[0].Expand["people"].(*Node); !ok {
		t.Fatalf("primary shape = %T", nodes[0].Expand["people"])
	}
	people, ok := nodes[1].Expand["people"].([]*Node)
	if !ok || len(people) != 2 {
		t.Fatalf("group shape = %T %#v", nodes[1].Expand["people"], nodes[1].Expand["people"])
	}
}

func TestExpandRejectsCorruptTargetType(t *testing.T) {
	ts := newTypes(t, `
types:
  person:
    fields: [{ name: name, kind: text }]
  article:
    fields: [{ name: author, kind: ref, to: person }]
`)
	s := New(testDB(t), ts)
	article, _ := s.CreateNode(&Node{Type: "article", Display: "文章", Fields: Fields{}})
	wrong, _ := s.CreateNode(&Node{Type: "article", Display: "错误目标", Fields: Fields{}})
	_, err := s.db.Insert("edges", map[string]any{
		"from_node": article, "field": "author", "to_node": wrong,
		"sort": 0, "created_at": time.Now(),
	}).Exec()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Expand(t.Context(), article, gquery.Expand(gquery.Ref("author")))
	if err == nil {
		t.Fatal("corrupt target type must fail")
	}
}

// 爆炸防护: 单字段超 1000 引用 → fail-loud（不静默截断）。
func TestExpandOverflowFails(t *testing.T) {
	s := newFilterSvc(t)
	// 1500 个作者 + 1 篇文章挂满（绕过 title 用 categories? — article 有 categories ref[]）
	ids := make([]any, 0, 1500)
	for i := 0; i < 1500; i++ {
		id, err := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "c" + strconv.Itoa(i)}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	aid, err := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "big", "categories": ids}})
	if err != nil {
		t.Fatal(err)
	}
	// 单节点版
	if _, err := expandText(t, s, aid, "categories"); err == nil {
		t.Fatal("single: 1500 refs must fail (not silently truncate)")
	}
	// 批量版
	_, err = expandManyText(t, s, []int64{aid}, "categories")
	if err == nil {
		t.Fatal("batch: 1500 refs must fail")
	}
}

// 爆炸防护: 链深超 4 段 → fail-loud。
func TestExpandDeepPathFails(t *testing.T) {
	s := newFilterSvc(t)
	p, _ := s.CreateNode(&Node{Type: "person", Display: "t", Fields: Fields{"name": "张三"}})
	a, _ := s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "x", "authors": []any{p}}})
	_, err := expandText(t, s, a, "authors.authors.authors.authors.authors")
	if err == nil {
		t.Fatal("5-segment path must fail (max 4)")
	}
	paths := make([]string, gquery.MaxExpandPaths+1)
	for i := range paths {
		paths[i] = "authors"
	}
	_, err = expandText(t, s, a, strings.Join(paths, ","))
	if err == nil || !strings.Contains(err.Error(), "paths") {
		t.Fatalf("too many expand paths must fail: %v", err)
	}
}

// 出边和明确来源类型的入边可并行展开，响应 key 不冲突。
func TestExpandBidirectionalKey(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "子", "parent": root}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "a", "categories": []any{child}}})

	n, err := expandText(t, s, child, "parent, <-article.categories")
	if err != nil {
		t.Fatal(err)
	}
	parent, hasParent := n.Expand["parent"].(*Node)
	incoming, hasIncoming := n.Expand["<-article.categories"].([]*Node)
	if !hasParent || parent.ID != root || !hasIncoming || len(incoming) != 1 {
		t.Fatalf("expansion = %#v", n.Expand)
	}

	nodes, err := expandManyText(t, s, []int64{child}, "parent, <-article.categories")
	if err != nil || len(nodes) != 1 {
		t.Fatal(err)
	}
	if _, ok := nodes[0].Expand["<-article.categories"]; !ok {
		t.Fatal("batch incoming key missing")
	}
}

// 多层展开（三层出边链 / 多层入边 / 批量）: 每层递归挂载, 方向每段独立。
func TestExpandMultiLevel(t *testing.T) {
	s := newFilterSvc(t)
	root, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "根"}})
	child, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "子", "parent": root}})
	grand, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "孙", "parent": child}})
	great, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "重孙", "parent": grand}})
	art, _ := s.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "a", "categories": []any{grand}}})

	// 三层出边链: 文章 → categories(grand) → parent(child) → parent(root)
	n, err := expandText(t, s, art, "categories.parent.parent")
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
	m, err := expandText(t, s, child, "<-category.parent.<-category.parent")
	if err != nil {
		t.Fatal(err)
	}
	in1 := m.Expand["<-category.parent"].([]*Node)
	if len(in1) != 1 || in1[0].ID != grand {
		t.Fatalf("in level1: %v", in1)
	}
	in2 := in1[0].Expand["<-category.parent"].([]*Node)
	if len(in2) != 1 || in2[0].ID != great {
		t.Fatalf("in level2: %v", in2)
	}

	// 混合根类型不能偷用其他 Type 的同名字段，必须 fail-loud。
	_, err = expandManyText(t, s, []int64{art, child}, "categories.parent")
	if err == nil {
		t.Fatal("mixed root types with an invalid path must fail")
	}
}
