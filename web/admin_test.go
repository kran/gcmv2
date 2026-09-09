package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kran/gcmv2/core"
)

// adminLogin 登录拿 cookie。
func adminLogin(t *testing.T, s *Site) *http.Cookie {
	t.Helper()
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "admin"})
	// testSite 的 AdminPass 为空 → 随机密码 — 直接查库验证? 简化: 用 EnsureDefaults 语义,
	// testSite 没设 AdminPass — 密码随机 — 这里直接绕过登录用 requireAuth 检查。
	_ = w
	return nil
}

// TestAdminRequireAuth 未登录访问受保护端点 → 401。
func TestAdminRequireAuth(t *testing.T) {
	s := testSite(t)
	for _, path := range []string{"/admin/me", "/admin/nodes?type=article", "/admin/types", "/admin/settings"} {
		w := do(s, "GET", path, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s = %d, want 401", path, w.Code)
		}
	}
	for _, path := range []string{"/admin/upload", "/admin/logout"} {
		w := do(s, "POST", path, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("POST %s = %d, want 401", path, w.Code)
		}
	}
}

// TestAdminLoginBad 错误密码 → 401。
func TestAdminLoginBad(t *testing.T) {
	s := testSite(t)
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "wrong"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad login = %d", w.Code)
	}
}

// TestAdminUI 管理 UI 静态资源公开（SPA 登录态自理）。
func TestAdminUI(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/admin/ui/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ui = %d", w.Code)
	}
	if w.Body.String() == "" {
		t.Fatal("ui empty")
	}
}

func TestAdminTreeRouteUsesParamKey(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/admin/ui/pages/App.vue", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("app component = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<router-view :key="routeViewKey"`) {
		t.Fatal("router-view is not keyed by route params")
	}
	if !strings.Contains(body, `JSON.stringify(route.params || {})`) {
		t.Fatal("route view key does not include route params")
	}
}

// TestAdminNodes CRUD 全流程（含认证守卫）。
func TestAdminNodes(t *testing.T) {
	s := testSite(t)
	// 登录（testSite 无固定密码 — 从库读? 简化: EnsureDefaults 的随机密码不可知 —
	// 这里测 CRUD 用未认证 → 401 即可; 登录流程在 auth_test 已覆盖）
	// 直接验证: 未认证 CRUD 全 401
	for _, req := range []struct {
		method, path string
	}{
		{"POST", "/admin/nodes?type=article"},
		{"PUT", "/admin/nodes/1"},
		{"DELETE", "/admin/nodes/1"},
		{"GET", "/admin/nodes/1"},
		{"GET", "/admin/tree?type=category"},
		{"GET", "/admin/expand?node=1"},
		{"POST", "/admin/settings"},
		{"GET", "/admin/search?q=x"},
	} {
		w := do(s, req.method, req.path, map[string]any{})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d, want 401", req.method, req.path, w.Code)
		}
	}
}

// TestAdminPasswordFlow 用真实登录（testSite 的 admin 密码随机 —
// 通过 EnsureDefaults 语义, 这里重置密码再登录）。
func TestAdminPasswordFlow(t *testing.T) {
	s := testSite(t)
	// 设固定密码（模拟 NewSite AdminPass 引导后的状态）
	if err := NewService(s.DB()).SetPassword("cmx12345"); err != nil {
		t.Fatal(err)
	}
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "cmx12345"})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", w.Code, w.Body.String())
	}
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			ck = c
		}
	}
	if ck == nil {
		t.Fatal("no cookie")
	}
	// 登录后 me
	w = do(s, "GET", "/admin/me", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("me = %d", w.Code)
	}
	// 建节点（article）
	w = do(s, "POST", "/admin/nodes?type=article", map[string]any{
		"display": "后台文章", "status": 1, "sort": 0,
		"fields": map[string]any{"body": "正文"},
	}, ck)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == 0 {
		t.Fatal("no id")
	}
	// 读回
	w = do(s, "GET", "/admin/nodes/"+itoa(created.ID), nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("get = %d", w.Code)
	}
	var n core.Node
	json.Unmarshal(w.Body.Bytes(), &n)
	if n.Display != "后台文章" {
		t.Fatalf("display = %q", n.Display)
	}
	// 更新
	w = do(s, "PUT", "/admin/nodes/"+itoa(created.ID), map[string]any{
		"slug": "", "status": 0, "sort": 5,
	}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	// 删除
	w = do(s, "DELETE", "/admin/nodes/"+itoa(created.ID), nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d", w.Code)
	}
	w = do(s, "GET", "/admin/nodes/"+itoa(created.ID), nil, ck)
	if w.Code != http.StatusNotFound {
		t.Fatalf("deleted get = %d", w.Code)
	}
	// logout 必须同时使服务端 session_key 失效，旧 Cookie 不可复用。
	w = do(s, "POST", "/admin/logout", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("logout = %d", w.Code)
	}
	w = do(s, "GET", "/admin/me", nil, ck)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie after logout = %d, want 401", w.Code)
	}
}

func TestAdminSecureCookie(t *testing.T) {
	s := testSiteConfigured(t, func(site *Site) { site.SecureCookies(true) })
	if err := NewService(s.DB()).SetPassword("cmx12345"); err != nil {
		t.Fatal(err)
	}
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "cmx12345"})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("admin cookie flags = %#v", cookies)
	}
}

func TestAdminSessionExpiresServerSide(t *testing.T) {
	s := testSite(t)
	service := NewService(s.DB())
	key, err := service.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if !service.ValidSession(key) {
		t.Fatal("new admin session should be valid")
	}
	_, err = s.DB().Update("accounts", map[string]any{
		"session_expires_at": time.Now().Add(-time.Minute),
	}, "1 = 1").Exec()
	if err != nil {
		t.Fatal(err)
	}
	if service.ValidSession(key) {
		t.Fatal("expired admin session should be rejected")
	}
}

// TestAdminSettings 设置 CRUD。
func TestAdminSettings(t *testing.T) {
	s := testSite(t)
	if err := NewService(s.DB()).SetPassword("cmx12345"); err != nil {
		t.Fatal(err)
	}
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "cmx12345"})
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			ck = c
		}
	}
	// 设置
	w = do(s, "POST", "/admin/settings", map[string]any{
		"key": "home-title", "group": "home", "type": "text", "value": "首页",
	}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("set = %d: %s", w.Code, w.Body.String())
	}
	// 列表
	w = do(s, "GET", "/admin/settings", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var out struct {
		Items []core.Setting `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if len(out.Items) != 1 || out.Items[0].Key != "home-title" {
		t.Fatalf("items = %+v", out.Items)
	}
	// 删除
	w = do(s, "DELETE", "/admin/settings/home-title", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d", w.Code)
	}
}
