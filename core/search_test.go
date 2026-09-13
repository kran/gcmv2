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
	publishedID, _ := service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "人工智能与制造业", "body": "深度融合路径研究", "publication_state": "published"}})
	draftID, _ := service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
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

	err = service.DeleteNode(t.Context(), draftID)
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
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "人工智能与制造业", "body": "产业路径研究", "publication_state": "published"}})
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "区域规划", "body": "2026 年规划报告", "publication_state": "published"}})
	service.CreateNode(t.Context(), &Node{Type: "person", Display: "t",
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
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "重建测试", "body": "x", "publication_state": "published"}})
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "重建草稿", "body": "x", "publication_state": "draft"}})
	_, err := service.db.Add("DELETE FROM nodes_fts").Exec()
	if err != nil {
		t.Fatal(err)
	}
	_, total, _ := searchOneType(t, service, "重建", "article", BypassPolicy())
	if total != 0 {
		t.Fatal("precondition: index empty")
	}
	if err := service.search.Rebuild(t.Context()); err != nil {
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
	if !strings.Contains(text, "正文") || strings.Contains(text, "5") {
		t.Fatalf("searchableText: %q", text)
	}
	// display 不进 body_text: 它由 nodes_fts 的 display 列承载（bm25 权重最高），
	// 所以既不需要也不允许写进 searchable.fields。
	if strings.Contains(text, "标题") {
		t.Fatalf("display 不该进 body_text: %q", text)
	}
}

// display 是节点列, 索引时无条件进 FTS 的 display 列 —— 不必声明, 也搜得到。
func TestSearchFindsDisplayWithoutDeclaring(t *testing.T) {
	const label = "商会观察"
	service := newFilterSvc(t)
	if _, err := service.CreateNode(t.Context(), &Node{
		Type: "article", Display: label, Fields: Fields{"publication_state": "published"},
	}); err != nil {
		t.Fatal(err)
	}
	// display 只在 display 列里（searchable.fields 是 [title, body], 都没填）
	_, total, err := searchOneType(t, service, label, "article", PolicyScope(gquery.EQ(
		gquery.Field("publication_state"), "published")))
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("按 display 搜不到（display 列应默认进索引）: total=%d", total)
	}
	if text := service.searchableText(&Node{Type: "article", Display: label}); text != "" {
		t.Fatalf("display 不该进 body_text: %q", text)
	}
}

// 查询切出来的 bigram 会跨词边界（"农业著名" → 农业/业著/著名）。文章里没有连续的
// "农业著名" 时既不能整条归零, 也不能把只含一个词的算进来: 丢掉语料里不存在的 bigram
// （"业著"是接缝, 语料里查无此词）再 AND, 得到的就是"同时含这两个词"。
func TestSearchMatchesAllWordsWhenBigramsSpanBoundaries(t *testing.T) {
	service := newFilterSvc(t)
	both, _ := service.CreateNode(t.Context(), &Node{Type: "article", Display: "both",
		Fields: Fields{"title": "农业现代化", "body": "著名学者", "publication_state": "published"}})
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "one",
		Fields: Fields{"title": "农业发展", "body": "区域经济", "publication_state": "published"}})

	// 连续子串仍然走精确路径
	if _, total, _ := searchOneType(t, service, "农业现代", "article", BypassPolicy()); total != 1 {
		t.Fatalf("substring: total=%d, want 1", total)
	}
	rows, total, err := searchOneType(t, service, "农业著名", "article", BypassPolicy())
	if err != nil {
		t.Fatalf("widened search: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != both {
		t.Fatalf("只该命中同时含两个词的那篇: total=%d rows=%d", total, len(rows))
	}
}

// 接缝 bigram 恰好也出现在语料里时, AND 会太严（"农业 业著 著名" 要求三者同时有）。
// 这时退到 OR: 宁可多给, 也不要空手。
func TestSearchFallsBackToOrWhenBoundaryBigramExists(t *testing.T) {
	service := newFilterSvc(t)
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "seam",
		Fields: Fields{"title": "企业著称", "body": "区域经济", "publication_state": "published"}})
	both, _ := service.CreateNode(t.Context(), &Node{Type: "article", Display: "both",
		Fields: Fields{"title": "农业现代化", "body": "著名学者", "publication_state": "published"}})

	rows, total, err := searchOneType(t, service, "农业著名", "article", BypassPolicy())
	if err != nil {
		t.Fatalf("fallback search: %v", err)
	}
	if total == 0 {
		t.Fatal("退到 OR 后不该空手")
	}
	var found bool
	for _, r := range rows {
		if r.ID == both {
			found = true
		}
	}
	if !found {
		t.Fatalf("同时含两个词的那篇应该在结果里: total=%d", total)
	}
}

// 用户输入里的引号是普通字符, 不该破坏 FTS 语法（原来搜一个 " 就是 500）。
func TestSearchTreatsQuotesAsLiteral(t *testing.T) {
	service := newFilterSvc(t)
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "人工智能", "body": "产业路径", "publication_state": "published"}})
	if _, _, err := searchOneType(t, service, `"`, "article", BypassPolicy()); err != nil {
		t.Fatalf("lone quote: %v", err)
	}
	if _, total, err := searchOneType(t, service, `"人工智能`, "article", BypassPolicy()); err != nil || total != 1 {
		t.Fatalf(`quoted query: total=%d err=%v`, total, err)
	}
}

// CountExact 走的是不带 LIMIT 的计数分支（countMatches 里 AddIf 的 false 一侧）。
func TestSearchExactCount(t *testing.T) {
	service := newFilterSvc(t)
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "t",
		Fields: Fields{"title": "人工智能", "body": "产业路径", "publication_state": "published"}})
	rows, total, err := service.Search(t.Context(), SearchQuery{
		Text:       "人工智能",
		Targets:    []SearchTarget{{Type: "article", Scope: BypassPolicy()}},
		CountLimit: CountExact,
		Page:       gquery.Page{Number: 1, Size: 10},
	})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("exact count: rows=%d total=%d err=%v", len(rows), total, err)
	}
}

// 用户输入不能变成 FTS5 的语法。每个词元都被包成短语, 所以关键字/操作符只是普通词。
func TestSearchTreatsFTSOperatorsAsTerms(t *testing.T) {
	service := newFilterSvc(t)
	keywords, _ := service.CreateNode(t.Context(), &Node{Type: "article", Display: "kw",
		Fields: Fields{"title": "t", "body": "AND OR NOT NEAR", "publication_state": "published"}})
	service.CreateNode(t.Context(), &Node{Type: "article", Display: "abc",
		Fields: Fields{"title": "t", "body": "abc def", "publication_state": "published"}})

	// AND 只当词: 命中含该词的文档, 而不是"语法错"或"命中全部"
	if _, total, err := searchOneType(t, service, "AND", "article", BypassPolicy()); err != nil || total != 1 {
		t.Fatalf("AND 被当成操作符: total=%d err=%v", total, err)
	}
	// 两个关键字当作相邻短语（不是"AND OR"这样的表达式）
	rows, total, err := searchOneType(t, service, "AND OR", "article", BypassPolicy())
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != keywords {
		t.Fatalf("AND OR 应该按短语匹配: total=%d err=%v", total, err)
	}
	// 列过滤语法不该生效（生效会报 no such column）
	if _, _, err := searchOneType(t, service, "a:b", "article", BypassPolicy()); err != nil {
		t.Fatalf("a:b 被当成列过滤: %v", err)
	}
	// 前缀查询语法不该生效（abc* 退化成普通词 abc）
	if _, total, err := searchOneType(t, service, "abc*", "article", BypassPolicy()); err != nil || total != 1 {
		t.Fatalf("abc* 应该退化成普通词: total=%d err=%v", total, err)
	}
}
