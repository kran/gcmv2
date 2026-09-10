package core

import (
	"strings"
	"testing"

	gquery "github.com/kran/gcmv2/query"
)

func TestBigram(t *testing.T) {
	cases := map[string]string{
		"人工智能":   "人工 工智 智能",
		"科技":     "科技",
		"gcm 引擎": "gcm 引擎",
		"AI与产业":  "AI 与产 产业",
		"":       "",
		"中":      "中",
	}
	for input, want := range cases {
		if got := bigram(input); got != want {
			t.Fatalf("bigram(%q) = %q, want %q", input, got, want)
		}
	}
}

func searchOneType(t *testing.T, service *Service, text, typeName string, scope QueryScope) ([]Node, int64, error) {
	t.Helper()
	return service.Search(t.Context(), SearchQuery{
		Text:    text,
		Targets: []SearchTarget{{Type: typeName, Scope: scope}},
		Page:    gquery.Page{Number: 1, Size: 10},
	})
}

// Every active searchable Node is indexed. Publication is a query Policy, not
// an indexing decision.
func TestFTSSyncIsIndependentFromPublication(t *testing.T) {
	service := newFilterSvc(t)
	if _, ok := service.types.Searchable("article"); !ok {
		t.Fatal("test types must declare article searchable capability")
	}
	publishedID, _ := service.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "人工智能与制造业", "body": "深度融合路径研究", "publication_state": "published"}})
	draftID, _ := service.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "秘密草稿", "body": "不可搜", "publication_state": "draft"}})

	rows, total, err := searchOneType(t, service, "人工智能", "article", BypassPolicy())
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != publishedID {
		t.Fatalf("published search = %#v, total=%d err=%v", rows, total, err)
	}
	_, total, err = searchOneType(t, service, "秘密", "article", BypassPolicy())
	if err != nil || total != 1 {
		t.Fatalf("draft must be indexed: total=%d err=%v", total, err)
	}

	publicScope := PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
	_, total, err = searchOneType(t, service, "秘密", "article", publicScope)
	if err != nil || total != 0 {
		t.Fatalf("policy must hide draft: total=%d err=%v", total, err)
	}

	err = patchCurrent(t, service, draftID, &NodePatch{Fields: Fields{"publication_state": "published"}})
	if err != nil {
		t.Fatal(err)
	}
	_, total, err = searchOneType(t, service, "秘密", "article", publicScope)
	if err != nil || total != 1 {
		t.Fatalf("published policy visibility: total=%d err=%v", total, err)
	}

	err = patchCurrent(t, service, publishedID, &NodePatch{Fields: Fields{"publication_state": "draft"}})
	if err != nil {
		t.Fatal(err)
	}
	_, total, err = searchOneType(t, service, "人工智能", "article", BypassPolicy())
	if err != nil || total != 1 {
		t.Fatalf("draft remains indexed: total=%d err=%v", total, err)
	}
	_, total, err = searchOneType(t, service, "人工智能", "article", publicScope)
	if err != nil || total != 0 {
		t.Fatalf("policy must hide unpublished node: total=%d err=%v", total, err)
	}

	err = service.DeleteNode(draftID)
	if err != nil {
		t.Fatal(err)
	}
	_, total, err = searchOneType(t, service, "秘密", "article", BypassPolicy())
	if err != nil || total != 0 {
		t.Fatalf("deleted node remains indexed: total=%d err=%v", total, err)
	}
}

func TestFTSQueryWithTypedTargets(t *testing.T) {
	service := newFilterSvc(t)
	service.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "人工智能与制造业", "body": "产业路径研究", "publication_state": "published"}})
	service.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "区域规划", "body": "2026 年规划报告", "publication_state": "published"}})
	service.CreateNode(&Node{Type: "person", Display: "t",
		Fields: Fields{"name": "人工智能专家", "publication_state": "published"}})

	_, total, err := searchOneType(t, service, "人工智能", "article", BypassPolicy())
	if err != nil || total != 1 {
		t.Fatalf("phrase exact: total=%d err=%v", total, err)
	}
	_, total, _ = searchOneType(t, service, "规划", "article", BypassPolicy())
	if total != 1 {
		t.Fatalf("2-char: total=%d", total)
	}
	_, total, _ = searchOneType(t, service, "人工智能", "person", BypassPolicy())
	if total != 1 {
		t.Fatalf("type filter: total=%d", total)
	}
	_, total, _ = searchOneType(t, service, "2026", "article", BypassPolicy())
	if total != 1 {
		t.Fatalf("ascii: total=%d", total)
	}

	rows, total, err := service.Search(t.Context(), SearchQuery{
		Text: "人工智能",
		Targets: []SearchTarget{
			{Type: "article", Scope: BypassPolicy()},
			{Type: "person", Scope: BypassPolicy()},
		},
		Page: gquery.Page{Number: 1, Size: 10},
	})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("multi-type search = %#v, total=%d err=%v", rows, total, err)
	}
}

func TestSearchRequiresPolicyScope(t *testing.T) {
	service := newFilterSvc(t)
	_, _, err := service.Search(t.Context(), SearchQuery{
		Text: "test", Targets: []SearchTarget{{Type: "article"}},
		Page: gquery.Page{Size: 10},
	})
	if err == nil {
		t.Fatal("search without policy scope must fail")
	}
}

func TestFTSRebuild(t *testing.T) {
	service := newFilterSvc(t)
	service.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "重建测试", "body": "x", "publication_state": "published"}})
	service.CreateNode(&Node{Type: "article", Display: "t",
		Fields: Fields{"title": "重建草稿", "body": "x", "publication_state": "draft"}})
	_, err := service.db.Add("DELETE FROM nodes_fts").Exec()
	if err != nil {
		t.Fatal(err)
	}
	_, total, _ := searchOneType(t, service, "重建", "article", BypassPolicy())
	if total != 0 {
		t.Fatal("precondition: index empty")
	}
	if err := service.search.Rebuild(); err != nil {
		t.Fatal(err)
	}
	_, total, _ = searchOneType(t, service, "重建", "article", BypassPolicy())
	if total != 2 {
		t.Fatalf("rebuild must index published and draft nodes: total=%d", total)
	}
	publicScope := PolicyScope(gquery.EQ(gquery.Field("publication_state"), "published"))
	_, total, _ = searchOneType(t, service, "重建", "article", publicScope)
	if total != 1 {
		t.Fatalf("publication policy after rebuild: total=%d", total)
	}
}

func TestSearchableText(t *testing.T) {
	service := newFilterSvc(t)
	node := &Node{Type: "article", Display: "标题", Fields: Fields{"body": "正文", "authors": []any{5}}}
	text := service.searchableText(node)
	if !strings.Contains(text, "标题") || !strings.Contains(text, "正文") || strings.Contains(text, "5") {
		t.Fatalf("searchableText: %q", text)
	}
}
