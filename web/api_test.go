package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/kran/gcmv2/core"
)

// TestApiNodesList 列表 + 类型过滤 + 分页。
func TestApiNodesList(t *testing.T) {
	s := testSite(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Engine().CreateNode(&core.Node{
			Type: "article", Display: "文章", Status: 1,
			Fields: map[string]any{"body": "内容"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	w := do(s, "GET", "/api/nodes/article?size=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("api = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Items []core.Node `json:"items"`
		Total int64       `json:"total"`
		Page  int         `json:"page"`
		Size  int         `json:"size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 || len(out.Items) != 2 {
		t.Fatalf("items=%d total=%d", len(out.Items), out.Total)
	}
}

// TestApiNodesFilter Lisp filter 直通（type 合成 + 自定义条件）。
func TestApiNodesFilter(t *testing.T) {
	s := testSite(t)
	s.Engine().CreateNode(&core.Node{Type: "article", Display: "甲", Status: 1,
		Fields: map[string]any{"body": "x"}})
	s.Engine().CreateNode(&core.Node{Type: "article", Display: "乙", Status: 1,
		Fields: map[string]any{"body": "y"}})
	w := do(s, "GET", "/api/nodes/article?filter="+url.QueryEscape(`(= $body "x")`), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("filter api = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Total int64 `json:"total"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Total != 1 {
		t.Fatalf("filter total = %d", out.Total)
	}
}

// TestApiNodesBadFilter 编译错误 → 400。
func TestApiNodesBadFilter(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/article?filter="+url.QueryEscape(`(bad-op 1)`), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad filter = %d", w.Code)
	}
}

// TestApiNodesUnknownType 未知类型 → 正常空列表（type 过滤无结果）。
func TestApiNodesUnknownType(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/nonexistent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown type = %d", w.Code)
	}
}

// TestApiNodesMissingType 缺 type 段 → 路由 404。
func TestApiNodesMissingType(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing type = %d", w.Code)
	}
}
