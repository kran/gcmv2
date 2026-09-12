package web

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// 模板 helper 与 API 用同一套读规则：
//   - 有 publication 的类型不注册规则 → 只读已发布（默认）
//   - 站点注册了规则 → 模板按规则（而不是框架私有拷贝）过滤
//   - 非 publication 类型：站点注册规则后模板才拿得到数据（之前直接 panic）
func TestRenderHelpersUseReadRules(t *testing.T) {
	dir := t.TempDir()
	typesYAML := `
types:
  article:
    capabilities:
      publication: { field: publication_state, draft: draft, published: published }
    fields:
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: title, kind: text }
  guestbook:
    capabilities:
      searchable: { fields: [title] }
    fields:
      - { name: title, kind: text }
`
	if err := os.WriteFile(filepath.Join(dir, "types.yaml"), []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tdir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// get 1 = 已发布 article; get 2 = 草稿 article; get 3 = visible guestbook; get 4 = hidden guestbook
	home := `articles:{{ range list "article" 1 10 }}{{ .Display }},{{ end }}|` +
		`guestbook:{{ range list "guestbook" 1 10 }}{{ .Display }},{{ end }}|` +
		`get-published:{{ with get 1 }}{{ .Display }}{{ end }}|` +
		`get-draft:{{ with get 2 }}{{ .Display }}{{ end }}|` +
		`get-visible-note:{{ with get 3 }}{{ .Display }}{{ end }}|` +
		`get-hidden-note:{{ with get 4 }}{{ .Display }}{{ end }}|` +
		`search:{{ range search "note" "" 1 10 }}{{ .Display }},{{ end }}`
	if err := os.WriteFile(filepath.Join(tdir, "home.html"), []byte(home), 0o644); err != nil {
		t.Fatal(err)
	}

	site := New(dir)
	// 站点规则：guestbook 只放行 title=visible 的行（列表与单节点都注册）。
	visibleOnly := func(_ *CmsCtx, _ string, expr *gquery.Expr, _ *core.List[string]) error {
		*expr = gquery.And(*expr, gquery.EQ(gquery.Field("title"), "visible"))
		return nil
	}
	site.ReadRule(ReadList, "guestbook", visibleOnly)
	site.ReadRule(ReadView, "guestbook", visibleOnly)
	site.ReadRule(ReadSearch, "guestbook", visibleOnly)
	site.Start()
	t.Cleanup(func() { _ = site.DB().Pool().Close() })

	create := func(node *core.Node) int64 {
		t.Helper()
		id, err := site.Engine().CreateNode(t.Context(), node)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	create(&core.Node{Type: "article", Display: "已发布",
		Fields: core.Fields{"publication_state": "published", "title": "已发布"}})
	create(&core.Node{Type: "article", Display: "草稿",
		Fields: core.Fields{"publication_state": "draft", "title": "草稿"}})
	create(&core.Node{Type: "guestbook", Display: "visible-note", Fields: core.Fields{"title": "visible"}})
	create(&core.Node{Type: "guestbook", Display: "hidden-note", Fields: core.Fields{"title": "hidden"}})

	w := do(site, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("home = %d: %s", w.Code, w.Body.String())
	}
	got := w.Body.String()
	want := "articles:已发布,|guestbook:visible-note,|" +
		"get-published:已发布|get-draft:|get-visible-note:visible-note|get-hidden-note:|" +
		"search:visible-note,"
	if got != want {
		t.Fatalf("home body = %q\nwant      = %q", got, want)
	}
}

// 不注册任何规则时，模板读到的是 publication 默认（非 publication 类型拿不到数据，
// 而不是像以前那样 panic）。
func TestRenderDefaultsToPublication(t *testing.T) {
	s := testSiteWithTemplates(t)
	id, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "已发布", Fields: core.Fields{"publication_state": "published"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "草稿", Fields: core.Fields{"publication_state": "draft"},
	}); err != nil {
		t.Fatal(err)
	}
	// 用 node.html 的公开详情路由验证：草稿不可见（404），已发布可见（200）。
	w := do(s, http.MethodGet, "/node/"+strconv.FormatInt(id, 10), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "已发布") {
		t.Fatalf("published node page = %d %q", w.Code, w.Body.String())
	}
}
