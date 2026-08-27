package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
    title: title
    create: 'false'
    fields:
      - { name: title, kind: text }
`
	tp := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(tp, []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "templates"), 0o755)
	site, err := NewSite(SiteSpec{
		DBPath:    filepath.Join(dir, "test.db"),
		Types:     tp,
		Templates: filepath.Join(dir, "templates"),
		Migrate:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
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

func TestAuthRegisterLoginMe(t *testing.T) {
	s := testSite(t)
	// 注册（注册即登录 — 响应带 token + cookie）
	w := do(s, "POST", "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
		"fields": map[string]any{"name": "张三"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			ID     int64          `json:"id"`
			Type   string         `json:"type"`
			Fields map[string]any `json:"fields"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" || out.User.ID == 0 {
		t.Fatalf("bad register response: %+v", out)
	}
	// cookie 也设了（双轨）
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookie {
			ck = c
		}
	}
	if ck == nil {
		t.Fatal("auth cookie not set")
	}
	// me（cookie 方式）
	w = do(s, "GET", "/api/auth/me", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("me(cookie) = %d", w.Code)
	}
	// me（Bearer 方式 — 同一个 token）
	req := httptest.NewRequest("GET", "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+out.Token)
	w2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w2, req)
	if w2.Code != http.StatusOK {
		t.Fatalf("me(bearer) = %d", w2.Code)
	}
	// 未登录 me → 401
	w = do(s, "GET", "/api/auth/me", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("me(anon) = %d", w.Code)
	}
}

func TestAuthLoginWrongPassword(t *testing.T) {
	s := testSite(t)
	do(s, "POST", "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	w := do(s, "POST", "/api/auth/login", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "wrong",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login(wrong) = %d", w.Code)
	}
	// 正确密码
	w = do(s, "POST", "/api/auth/login", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthRegisterDup(t *testing.T) {
	s := testSite(t)
	do(s, "POST", "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	w := do(s, "POST", "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password456",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dup register = %d", w.Code)
	}
}

func TestAuthCreateRule(t *testing.T) {
	s := testSite(t)
	// guestbook: create 'auth != nil' — 未登录拒绝
	w := do(s, "POST", "/api/nodes/guestbook", map[string]any{
		"fields": map[string]any{"title": "hi"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("anon create guestbook = %d", w.Code)
	}
	// article: create 'false' — 任何人拒绝（含登录）
	w = do(s, "POST", "/api/nodes/article", map[string]any{
		"fields": map[string]any{"title": "x"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("anon create article = %d", w.Code)
	}
	// 登录后 guestbook 可创建
	w = do(s, "POST", "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	var out struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	req := httptest.NewRequest("POST", "/api/nodes/guestbook", bytes.NewReader(
		[]byte(`{"fields":{"title":"留言"}}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+out.Token)
	w2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w2, req)
	if w2.Code != http.StatusCreated {
		t.Fatalf("authed create guestbook = %d: %s", w2.Code, w2.Body.String())
	}
	// 登录后 article 仍拒绝（create 'false'）
	req = httptest.NewRequest("POST", "/api/nodes/article", bytes.NewReader(
		[]byte(`{"fields":{"title":"x"}}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+out.Token)
	w3 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w3, req)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("authed create article = %d", w3.Code)
	}
}

func TestAuthLogout(t *testing.T) {
	s := testSite(t)
	w := do(s, "POST", "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookie {
			ck = c
		}
	}
	// 登出
	w = do(s, "POST", "/api/auth/logout", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("logout = %d", w.Code)
	}
	// 旧 cookie 失效
	w = do(s, "GET", "/api/auth/me", nil, ck)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d", w.Code)
	}
}
