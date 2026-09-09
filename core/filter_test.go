package core

import (
	"strings"
	"testing"
)

func TestFilterComplexityLimits(t *testing.T) {
	if _, err := parseLisp(strings.Repeat("x", maxFilterBytes+1)); err == nil {
		t.Fatal("oversized filter must fail")
	}
	deep := `(= status 1)`
	for range maxFilterDepth + 1 {
		deep = `(not ` + deep + `)`
	}
	if _, err := parseLisp(deep); err == nil {
		t.Fatal("deep filter must fail")
	}
	items := make([]string, maxArrayItems+1)
	for i := range items {
		items[i] = "1"
	}
	if _, err := parseLisp(`(in id [` + strings.Join(items, " ") + `])`); err == nil {
		t.Fatal("oversized array must fail")
	}
}

// ── 列 / JSON 比较 ────────────────────────────

func TestFilterColAndJSON(t *testing.T) {
	s := newTestService(t)
	s.CreateNode(&Node{Type: "article", Display: "t", Status: 1, Fields: Fields{"title": "甲", "views": 100}})
	s.CreateNode(&Node{Type: "article", Display: "t", Status: 0, Fields: Fields{"title": "乙", "views": 5}})

	// 列比较
	list, _, err := s.QueryPage(ListQuery{Filter: `(= status 1)`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "甲" {
		t.Fatalf("col filter: %v", list)
	}
	// JSON 比较
	list, _, err = s.QueryPage(ListQuery{Filter: `(= $title "乙")`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != 0 {
		t.Fatalf("json filter: %v", list)
	}
	// JSON 数值
	list, _, err = s.QueryPage(ListQuery{Filter: `(> $views 50)`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "甲" {
		t.Fatalf("json num filter: %v", list)
	}
}

// ── 逻辑 ──────────────────────────────────────

func TestFilterLogic(t *testing.T) {
	s := newTestService(t)
	s.CreateNode(&Node{Type: "article", Display: "t", Status: 1, Fields: Fields{"title": "甲", "views": 100}})
	s.CreateNode(&Node{Type: "article", Display: "t", Status: 1, Fields: Fields{"title": "乙", "views": 5}})
	s.CreateNode(&Node{Type: "article", Display: "t", Status: 0, Fields: Fields{"title": "丙", "views": 100}})

	// and
	list, _, _ := s.QueryPage(ListQuery{Filter: `(and (= status 1) (> $views 50))`, Page: 1, Size: 10})
	if len(list) != 1 || list[0].Fields["title"] != "甲" {
		t.Fatalf("and: %v", list)
	}
	// or
	list, _, _ = s.QueryPage(ListQuery{Filter: `(or (= status 0) (= $title "甲"))`, Page: 1, Size: 10})
	if len(list) != 2 {
		t.Fatalf("or: %d", len(list))
	}
	// not
	list, _, _ = s.QueryPage(ListQuery{Filter: `(not (= status 1))`, Page: 1, Size: 10})
	if len(list) != 1 || list[0].Fields["title"] != "丙" {
		t.Fatalf("not: %v", list)
	}
}

// ── 占位符 ────────────────────────────────────

func TestFilterPlaceholder(t *testing.T) {
	s := newTestService(t)
	s.CreateNode(&Node{Type: "article", Display: "t", Status: 1, Fields: Fields{"title": "甲"}})
	list, _, err := s.QueryPage(ListQuery{Filter: `(= status {:st})`, Page: 1, Size: 10},
		map[string]any{"st": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("placeholder: %d", len(list))
	}
	list, _, err = s.QueryPage(ListQuery{Filter: `(in status {:values})`, Page: 1, Size: 10},
		map[string]any{"values": []int64{1}})
	if err != nil || len(list) != 1 {
		t.Fatalf("slice placeholder: list=%d err=%v", len(list), err)
	}
	tooMany := make([]int64, maxArrayItems+1)
	_, _, err = s.QueryPage(ListQuery{Filter: `(in status {:values})`, Page: 1, Size: 10},
		map[string]any{"values": tooMany})
	if err == nil {
		t.Fatal("oversized placeholder collection should fail")
	}
	// 未绑定 → 报错
	_, _, err = s.QueryPage(ListQuery{Filter: `(= status {:nope})`, Page: 1, Size: 10})
	if err == nil {
		t.Fatal("unbound placeholder should fail")
	}
}

// ── 出边 / 入边 ───────────────────────────────

func TestFilterEdge(t *testing.T) {
	s := newTestService(t)
	cat, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "c"}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "挂c", "categories": []any{cat}}})
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "没挂"}})

	// 出边存在性
	list, _, err := s.QueryPage(ListQuery{Filter: `(edge ->categories)`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "挂c" {
		t.Fatalf("edge out: %v", list)
	}
	// 出边折叠（指向 cat）
	list, _, _ = s.QueryPage(ListQuery{Filter: `(edge ->categories 1)`, Page: 1, Size: 10})
	if len(list) != 1 {
		t.Fatalf("edge fold: %d", len(list))
	}
	// in 集合（categories 含 cat）
	list, _, _ = s.QueryPage(ListQuery{Filter: `(in ->categories [1])`, Page: 1, Size: 10})
	if len(list) != 1 {
		t.Fatalf("in out: %d", len(list))
	}
	// 入边存在性（category 被 article 指向）
	list, _, _ = s.QueryPage(ListQuery{Filter: `(edge <-article.categories)`, Page: 1, Size: 10})
	if len(list) != 1 || list[0].Type != "category" {
		t.Fatalf("edge in: %v", list)
	}
}

// ── 语法错误 ──────────────────────────────────

func TestFilterSyntaxErrors(t *testing.T) {
	s := newTestService(t)
	cases := []string{
		`(= status)`,     // 参数不足
		`(nope 1 2)`,     // 未知函数
		`(edge status)`,  // 非引用字段
		`(in status [1)`, // 未闭合
		`status`,         // 顶层非调用
	}
	for _, f := range cases {
		if _, _, err := s.QueryPage(ListQuery{Filter: f, Page: 1, Size: 10}); err == nil {
			t.Fatalf("filter %q should fail", f)
		}
	}
}

// ── 引号转义 ──────────────────────────────────

func TestFilterQuoteEscape(t *testing.T) {
	s := newTestService(t)
	s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": `他说"你好"`}})
	list, _, err := s.QueryPage(ListQuery{Filter: `(= $title "他说\"你好\"")`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("quote escape: %d", len(list))
	}
}

// subtree 集合函数: (in ->categories (subtree "root"))
func TestFilterSubtree(t *testing.T) {
	s := newTestService(t)
	root, _ := s.CreateNode(&Node{Type: "category", Display: "t", Slug: "root", Fields: Fields{"name": "root"}})
	a, _ := s.CreateNode(&Node{Type: "category", Display: "t", Fields: Fields{"name": "a", "parent": root}})
	_, _ = s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "挂a", "categories": []any{a}}})
	_, _ = s.CreateNode(&Node{Type: "article", Display: "t", Fields: Fields{"title": "没挂"}})

	list, _, err := s.QueryPage(ListQuery{Filter: `(in ->categories (subtree "root"))`, Page: 1, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Fields["title"] != "挂a" {
		t.Fatalf("subtree: %v", list)
	}
}
