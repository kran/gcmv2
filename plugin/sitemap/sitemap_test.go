package sitemap

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/web"
)

// testSite 建最小站点（带 sitemap 插件）。
func testSite(t *testing.T) *web.Site {
	t.Helper()
	dir := t.TempDir()
	tp := filepath.Join(dir, "types.yaml")
	os.WriteFile(tp, []byte(`
types:
  article:
    capabilities:
      addressable: { field: slug, unique: global }
      publication: { field: publication_state, draft: draft, published: published }
    fields:
      - { name: slug, kind: slug }
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: body, kind: richtext }
`), 0o644)
	tdir := filepath.Join(dir, "templates")
	os.MkdirAll(tdir, 0o755)
	site := web.New(dir)
	Mount(site, Options{BaseURL: "https://example.com"})
	site.Start()
	return site
}

func TestSitemap(t *testing.T) {
	s := testSite(t)
	eng := s.Engine()
	// 已发布（slug）
	eng.CreateNode(&core.Node{Type: "article", Display: "甲",
		Fields: map[string]any{"body": "x", "slug": "article-a", "publication_state": "published"}})
	// 已发布（无 slug → id）
	eng.CreateNode(&core.Node{Type: "article", Display: "乙",
		Fields: map[string]any{"body": "y", "publication_state": "published"}})
	// 草稿 — 不出现
	eng.CreateNode(&core.Node{Type: "article", Display: "草稿",
		Fields: map[string]any{"body": "z", "publication_state": "draft"}})

	req := mustReq(t, "GET", "/sitemap.xml")
	w := do(s, req)
	if w.code != http.StatusOK {
		t.Fatalf("sitemap = %d: %s", w.code, w.body.String())
	}
	body := w.body.String()
	for _, want := range []string{
		"<loc>https://example.com/</loc>",
		"<loc>https://example.com/node/article-a</loc>",
		"<loc>https://example.com/node/2</loc>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "草稿") {
		t.Fatal("draft must not appear")
	}
}

func mustReq(t *testing.T, method, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, "http://example.com"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func do(s *web.Site, req *http.Request) *httptestRecorder {
	w := newRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

type httptestRecorder struct {
	code int
	hdr  http.Header
	body strings.Builder
}

func newRecorder() *httptestRecorder { return &httptestRecorder{code: 200, hdr: http.Header{}} }

func (r *httptestRecorder) Header() http.Header { return r.hdr }

func (r *httptestRecorder) WriteHeader(code int) { r.code = code }

func (r *httptestRecorder) Write(b []byte) (int, error) { return r.body.Write(b) }
