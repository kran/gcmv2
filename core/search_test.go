package core

import (
	"strings"
	"testing"
)

// bigram 分词: CJK 滑窗 + 非 CJK 保留。
func TestBigram(t *testing.T) {
	cases := map[string]string{
		"人工智能":   "人工 工智 智能",
		"科技":     "科技",
		"gcm 引擎": "gcm 引擎",
		"AI与产业":  "AI 与产 产业",
		"":       "",
		"中":      "中",
	}
	for in, want := range cases {
		if got := bigram(in); got != want {
			t.Fatalf("bigram(%q) = %q, want %q", in, got, want)
		}
	}
}

// 索引同步: 可搜类型 + 已发布进索引; 草稿/不可搜类型不进; 更新状态双向迁移。
func TestFTSSync(t *testing.T) {
	s := newFilterSvc(t)
	// article search:true ✓（filterTypes 里 article 有 search: true? — 检查）
	td, _ := s.types.Type("article")
	if !td.Search {
		t.Fatal("test types must declare article search: true")
	}
	// 已发布文章 → 进索引
	id, _ := s.CreateNode(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "人工智能与制造业", "body": "深度融合路径研究"}})
	rows, total, err := s.Search("人工智能", "", 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("published must be searchable: total=%d", total)
	}
	// 草稿 → 不进
	draftID, _ := s.CreateNode(&Node{Type: "article", Status: StatusDraft,
		Fields: Fields{"title": "秘密草稿", "body": "不可搜"}})
	_, total, _ = s.Search("秘密", "", 1, 10)
	if total != 0 {
		t.Fatal("draft must not be indexed")
	}
	// 改状态: 草稿发布 → 进; 发布转草稿 → 出
	pub := StatusPublished
	if err := s.PatchNode(draftID, &NodePatch{Status: &pub}); err != nil {
		t.Fatal(err)
	}
	_, total, _ = s.Search("秘密", "", 1, 10)
	if total != 1 {
		t.Fatal("draft→published must enter index")
	}
	draft := StatusDraft
	if err := s.PatchNode(id, &NodePatch{Status: &draft}); err != nil {
		t.Fatal(err)
	}
	_, total, _ = s.Search("人工智能", "", 1, 10)
	if total != 0 {
		t.Fatal("published→draft must leave index")
	}
	// 删除 → 出索引
	if err := s.DeleteNode(draftID); err != nil {
		t.Fatal(err)
	}
	_, total, _ = s.Search("秘密", "", 1, 10)
	if total != 0 {
		t.Fatal("deleted must leave index")
	}
}

// 查询: 类型过滤 + 多词短语精确。
func TestFTSQuery(t *testing.T) {
	s := newFilterSvc(t)
	s.CreateNode(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "人工智能与制造业", "body": "产业路径研究"}})
	s.CreateNode(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "区域规划", "body": "2026 年规划报告"}})
	s.CreateNode(&Node{Type: "person", Status: StatusPublished,
		Fields: Fields{"name": "人工智能专家"}})

	// 多词 phrase: 连续 bigram 才命中
	_, total, err := s.Search("人工智能", "article", 1, 10)
	if err != nil || total != 1 {
		t.Fatalf("phrase exact: total=%d err=%v", total, err)
	}
	// 2 字词
	_, total, _ = s.Search("规划", "article", 1, 10)
	if total != 1 {
		t.Fatalf("2-char: total=%d", total)
	}
	// 类型过滤
	_, total, _ = s.Search("人工智能", "person", 1, 10)
	if total != 1 {
		t.Fatalf("type filter: total=%d", total)
	}
	// 英文数字
	_, total, _ = s.Search("2026", "article", 1, 10)
	if total != 1 {
		t.Fatalf("ascii: total=%d", total)
	}
}

// Rebuild: 全量重建（类型声明变化后）。
func TestFTSRebuild(t *testing.T) {
	s := newFilterSvc(t)
	s.CreateNode(&Node{Type: "article", Status: StatusPublished,
		Fields: Fields{"title": "重建测试", "body": "x"}})
	// 手动删索引模拟损坏
	if _, err := s.db.Add("DELETE FROM nodes_fts").Exec(); err != nil {
		t.Fatal(err)
	}
	_, total, _ := s.Search("重建", "", 1, 10)
	if total != 0 {
		t.Fatal("precondition: index empty")
	}
	if err := s.search.Rebuild(); err != nil {
		t.Fatal(err)
	}
	_, total, _ = s.Search("重建", "", 1, 10)
	if total != 1 {
		t.Fatalf("rebuild: total=%d", total)
	}
}

// 搜索文本拼接: 只拼标量字段（ref 值不进索引）。
func TestSearchableText(t *testing.T) {
	s := newFilterSvc(t)
	n := &Node{Type: "article", Title: "标题", Fields: Fields{"body": "正文", "authors": []any{5}}}
	text := s.searchableText(n)
	if !strings.Contains(text, "标题") || !strings.Contains(text, "正文") || strings.Contains(text, "5") {
		t.Fatalf("searchableText: %q", text)
	}
}
