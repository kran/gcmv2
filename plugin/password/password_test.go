// Package password plugin 测试 — register/login（email + 密码）。
package password

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kran/gcmv2/web"
)

// newSite 建临时站点（user auth:true + 模板目录）+ 装 password 插件。
func newSite(t *testing.T) *web.Site {
	t.Helper()
	dir := t.TempDir()
	typesYAML := `
types:
  user:
    auth: true
    fields:
      - { name: name, kind: text }
      - { name: role, kind: select, options: [member, editor] }
`
	tp := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(tp, []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "templates"), 0o755)
	site := web.New(dir)
	Mount(site)
	site.Start()
	return site
}

// post 请求 /api/auth/register|login，返回 recorder。
func post(site *web.Site, path string, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	site.Handler().ServeHTTP(w, req)
	return w
}

func TestPasswordRegisterLogin(t *testing.T) {
	s := newSite(t)
	w := post(s, "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
		"display": "张三", "fields": map[string]any{"name": "张三"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Token string `json:"token"`
		User  struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" || out.User.ID == 0 || out.User.Type != "user" {
		t.Fatalf("bad register response: %+v", out)
	}
	// 正确密码登录
	w = post(s, "/api/auth/login", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", w.Code, w.Body.String())
	}
	// 错误密码 → 401
	w = post(s, "/api/auth/login", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "wrong",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login(wrong) = %d", w.Code)
	}
}

func TestPasswordRegisterDup(t *testing.T) {
	s := newSite(t)
	post(s, "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
		"display": "a",
	})
	w := post(s, "/api/auth/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password456",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("dup register = %d", w.Code)
	}
}
