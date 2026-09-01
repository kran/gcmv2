package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
)

// testSite 临时站点（真实 DB + 最小类型）。
func testSite(t *testing.T) *Site {
	t.Helper()
	dir := t.TempDir()
	typesYAML := `
types:
  user:
    title: name
    auth: true
    fields:
      - { name: name, kind: text }
      - { name: role, kind: select, options: [member, editor] }
  guestbook:
    title: title
    create: 'auth != nil'
    fields:
      - { name: title, kind: text }
  article:
    create: 'false'
    fields:
      - { name: body, kind: richtext }
`
	tp := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(tp, []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "templates"), 0o755)
	site := New(dir)
	site.Start()
	return site
}

// do 请求 helper（返回 recorder）。
func do(site *Site, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var rd bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = *bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, &rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	site.Handler().ServeHTTP(w, req)
	return w
}

func TestAuthCreateRule(t *testing.T) {
	s := testSite(t)
	// 无 hook = 默认拒绝（安全）
	w := do(s, "POST", "/api/nodes/guestbook", map[string]any{"display": "hi"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("no hook create = %d", w.Code)
	}
	// AddHook 放行（站点深度权限 — 按类型）
	s.Engine().Hooks().AddHook(HookBeforeCreate, func(ctx *CmsCtx, node *core.Node) error {
		if node.Type == "article" {
			return errors.New("article not allowed")
		}
		return nil
	})
	// guestbook 放行 → 201
	w = do(s, "POST", "/api/nodes/guestbook", map[string]any{"display": "hi"})
	if w.Code != http.StatusCreated {
		t.Fatalf("allowed create = %d: %s", w.Code, w.Body.String())
	}
	// article 被 hook 拒绝 → 403
	w = do(s, "POST", "/api/nodes/article", map[string]any{"display": "x"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("deny create = %d", w.Code)
	}
	// 无 hook 更新/default 拒绝
	w = do(s, "PUT", "/api/nodes/guestbook/1", map[string]any{"display": "x"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("no hook update = %d", w.Code)
	}
}

func TestAuthLogout(t *testing.T) {
	s := testSite(t)
	ck := newSession(t, s)
	// 登出
	w := do(s, "POST", "/api/auth/logout", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("logout = %d", w.Code)
	}
	// 旧 cookie 失效
	w = do(s, "GET", "/api/auth/me", nil, ck)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d", w.Code)
	}
}

func TestAuthMe(t *testing.T) {
	s := testSite(t)
	ck := newSession(t, s)
	// me（cookie）
	if w := do(s, "GET", "/api/auth/me", nil, ck); w.Code != http.StatusOK {
		t.Fatalf("me(cookie) = %d", w.Code)
	}
	// 未登录 me → 401
	if w := do(s, "GET", "/api/auth/me", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("me(anon) = %d", w.Code)
	}
}

// newSession 建一个 user 节点 + auth（core.RegisterAuth）+ 会话（AuthSession），返回 cookie。
func newSession(t *testing.T, s *Site) *http.Cookie {
	t.Helper()
	n := &core.Node{Display: "a", Fields: core.Fields{"name": "a"}}
	id, err := s.Engine().RegisterAuth("user", "email", "a@x.com", core.Fields{"password": "x"}, n)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx := &CmsCtx{BaseContext: cho.MakeBaseContext(w, req), site: s}
	token, err := AuthSession(ctx, s.Engine(), id)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("empty token")
	}
	// 从响应 cookie 拿 authCookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookie {
			return c
		}
	}
	t.Fatal("auth cookie not set")
	return nil
}
