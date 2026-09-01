package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/gcmv2/core"
)

// testSiteWithTemplates 带模板目录的站点（404.html 等）。
func testSiteWithTemplates(t *testing.T) *Site {
	t.Helper()
	dir := t.TempDir()
	typesYAML := `
types:
  article:
    fields:
      - { name: body, kind: richtext }
  category:
    view: tree
    fields:
      - { name: name, kind: text }
      - { name: parent, kind: ref, to: category }
`
	tp := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(tp, []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(dir, "templates")
	os.MkdirAll(tdir, 0o755)
	os.WriteFile(filepath.Join(tdir, "node.html"),
		[]byte(`node:{{ .Node.Display }}|{{ .Node.ID }}`), 0o644)
	os.WriteFile(filepath.Join(tdir, "404.html"),
		[]byte(`404:{{ .Path }}`), 0o644)
	os.WriteFile(filepath.Join(tdir, "home.html"),
		[]byte(`home`), 0o644)
	site := New(dir)
	site.Start()
	return site
}

func TestDefaultHome(t *testing.T) {
	s := testSiteWithTemplates(t)
	w := do(s, "GET", "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("home = %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "home" {
		t.Fatalf("home body = %q", w.Body.String())
	}
}

func TestNodeHandlerByID(t *testing.T) {
	s := testSiteWithTemplates(t)
	id, err := s.Engine().CreateNode(&core.Node{Type: "article", Display: "文章一", Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/node/"+itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("node by id = %d: %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "node:文章一|"+itoa(id) {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestNodeHandlerBySlug(t *testing.T) {
	s := testSiteWithTemplates(t)
	id, err := s.Engine().CreateNode(&core.Node{Type: "article", Display: "文章二", Slug: "article-2", Status: 1})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/node/article-2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("node by slug = %d", w.Code)
	}
	if w.Body.String() != "node:文章二|"+itoa(id) {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestNodeHandlerDraft404(t *testing.T) {
	s := testSiteWithTemplates(t)
	id, _ := s.Engine().CreateNode(&core.Node{Type: "article", Display: "草稿", Status: 0})
	// 草稿 → 404
	w := do(s, "GET", "/node/"+itoa(id), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("draft = %d", w.Code)
	}
	if w.Body.String() != "404:/node/"+itoa(id) {
		t.Fatalf("404 body = %q", w.Body.String())
	}
}

func TestNodeHandlerMissing404(t *testing.T) {
	s := testSiteWithTemplates(t)
	w := do(s, "GET", "/node/99999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", w.Code)
	}
	// 无 404.html 的站点 → 纯文本
	s2 := testSite(t)
	w = do(s2, "GET", "/node/99999", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing(no tpl) = %d", w.Code)
	}
}

func TestNodeHandlerCandidates(t *testing.T) {
	// node--{type}.html 优先于 node.html
	s := testSiteWithTemplates(t)
	tdir := s.render.root
	os.WriteFile(filepath.Join(tdir, "node--article.html"),
		[]byte(`article-tpl:{{ .Node.Display }}`), 0o644)
	id, _ := s.Engine().CreateNode(&core.Node{Type: "article", Display: "候选", Status: 1})
	w := do(s, "GET", "/node/"+itoa(id), nil)
	if w.Body.String() != "article-tpl:候选" {
		t.Fatalf("candidate body = %q", w.Body.String())
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func TestDebugErrorPage(t *testing.T) {
	s := testSiteWithTemplates(t)
	s.debug = true
	// 破坏模板（语法错误）→ debug 详情页
	tdir := s.render.root
	os.WriteFile(filepath.Join(tdir, "node.html"), []byte(`{{ bad syntax`), 0o644)
	id, _ := s.Engine().CreateNode(&core.Node{Type: "article", Display: "x", Status: 1})
	w := do(s, "GET", "/node/"+itoa(id), nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("debug error = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Render Error") || !strings.Contains(body, "node.html") {
		t.Fatalf("debug page missing detail: %s", body[:min(len(body), 200)])
	}
	// 生产模式（默认）→ HTML 注释（fail-loud — 200 + 源码可见病灶）
	s.debug = false
	w = do(s, "GET", "/node/"+itoa(id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("prod error = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "render error") {
		t.Fatalf("prod body = %q", w.Body.String())
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
