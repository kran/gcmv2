package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
)

// testSite 临时站点（真实 DB + 最小类型）。
func testSite(t *testing.T) *Site {
	t.Helper()
	return testSiteConfigured(t, nil)
}

func testSiteConfigured(t *testing.T, configure func(*Site)) *Site {
	t.Helper()
	dir := t.TempDir()
	typesYAML := `
types:
  user:
    capabilities:
      authentication: true
    fields:
      - { name: name, kind: text }
      - { name: role, kind: select, options: [member, editor] }
  staff:
    capabilities:
      authentication: true
    fields:
      - { name: name, kind: text }
  guestbook:
    fields:
      - { name: title, kind: text }
  article:
    capabilities:
      publication: { field: publication_state, draft: draft, published: published }
    fields:
      - { name: publication_state, kind: select, options: [draft, published], default: draft }
      - { name: body, kind: richtext }
`
	tp := filepath.Join(dir, "types.yaml")
	if err := os.WriteFile(tp, []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "templates"), 0o755)
	site := New(dir)
	site.Auth().Register(AuthRealm{
		Name: "members", NodeType: "user", AllowRegister: true, Default: true,
	})
	if configure != nil {
		configure(site)
	}
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

func TestWriteRulesGateNodeWrites(t *testing.T) {
	s := testSite(t)
	cookie := newSession(t, s)
	// 未注册规则 = 拒绝。匿名调用者得到 401，已认证调用者得到 403。
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/nodes/guestbook", map[string]any{"display": "hi"}},
		{http.MethodPut, "/api/nodes/guestbook/1", map[string]any{"display": "x"}},
		{http.MethodDelete, "/api/nodes/guestbook/1", nil},
	} {
		w := do(s, tc.method, tc.path, tc.body)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s = %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if code := errorCode(t, w); code != string(CodeUnauthorized) {
			t.Fatalf("anonymous %s %s code = %q", tc.method, tc.path, code)
		}
		w = do(s, tc.method, tc.path, tc.body, cookie)
		if w.Code != http.StatusForbidden {
			t.Fatalf("authenticated %s %s = %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		if code := errorCode(t, w); code != string(CodeForbidden) {
			t.Fatalf("authenticated %s %s code = %q", tc.method, tc.path, code)
		}
	}
}

// TestWriteRulesBoundFieldsAndTypes 写规则同时限定类型、动作与可提交字段；
// 白名单只约束客户端，规则自己可以写客户端不可提交的字段。
func TestWriteRulesBoundFieldsAndTypes(t *testing.T) {
	s := testSiteConfigured(t, func(site *Site) {
		site.WriteRule(WriteCreate, "article", func(_ *CmsCtx, node *core.Node, allow *core.List[string]) error {
			allow.Append("body")
			node.Fields["publication_state"] = "published" // 规则写客户端不可提交的字段
			return nil
		})
		site.WriteRule(WriteUpdate, "article", func(_ *CmsCtx, _ int64, _ *core.NodePatch, allow *core.List[string]) error {
			allow.Append("body")
			return nil
		})
		site.WriteRule(WriteDelete, "article", func(*CmsCtx, int64) error { return nil })
	})
	// 白名单外的字段 → 422 + details 指名（仅限越权字段）。
	w := do(s, http.MethodPost, "/api/nodes/article", map[string]any{
		"display": "hi", "fields": map[string]any{"body": "b", "publication_state": "draft"},
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("undeclared field = %d %s", w.Code, w.Body.String())
	}
	var failure struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	}
	decodeBody(t, w, &failure)
	if failure.Code != string(CodeInvalidValue) || failure.Details["publication_state"] == "" {
		t.Fatalf("undeclared field payload = %#v", failure)
	}
	if _, reported := failure.Details["body"]; reported {
		t.Fatalf("declared field reported as rejected: %#v", failure.Details)
	}
	// 白名单内 → 201，且 Hook 写入的 publication_state 生效。
	w = do(s, http.MethodPost, "/api/nodes/article", map[string]any{
		"display": "hi", "fields": map[string]any{"body": "b"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("declared field = %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID   int64 `json:"id"`
		Node struct {
			Revision int64 `json:"revision"`
		} `json:"node"`
	}
	decodeBody(t, w, &created)
	full, err := s.Engine().FullNode(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := full.Values.Str("publication_state"); got != "published" {
		t.Fatalf("hook field = %q, want published", got)
	}
	// 注册过的 update 同样只接受白名单字段。
	w = do(s, http.MethodPut, "/api/nodes/article/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"revision": created.Node.Revision, "fields": map[string]any{"publication_state": "draft"},
	})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("update undeclared field = %d %s", w.Code, w.Body.String())
	}
	w = do(s, http.MethodPut, "/api/nodes/article/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"revision": created.Node.Revision, "fields": map[string]any{"body": "renamed"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update declared field = %d %s", w.Code, w.Body.String())
	}
	// 未注册规则的 update 也不带规则也要拒绝，且拒绝先于存在性判断。
	w = do(s, http.MethodPut, "/api/nodes/guestbook/999999", map[string]any{"display": "x"}, newSession(t, s))
	if w.Code != http.StatusForbidden {
		t.Fatalf("undeclared type update = %d %s", w.Code, w.Body.String())
	}
	// 删除：注册过 → 归档成功。
	w = do(s, http.MethodDelete, "/api/nodes/article/"+strconv.FormatInt(created.ID, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("declared delete = %d %s", w.Code, w.Body.String())
	}
}

func TestAuthSessionSecureCookie(t *testing.T) {
	s := testSiteConfigured(t, func(site *Site) { site.SecureCookies(true) })
	id, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "user", Display: "secure", Fields: core.Fields{"name": "secure"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	ctx := s.CmsCtxMaker(response, request)
	realm := s.Auth().MustRealm("members")
	if _, err := AuthSession(ctx, realm, id); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].Secure || !cookies[0].HttpOnly {
		t.Fatalf("auth cookie flags = %#v", cookies)
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

func TestAuthActorAndPrincipal(t *testing.T) {
	s := testSite(t)
	cookie := newSession(t, s)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	ctx := s.CmsCtxMaker(httptest.NewRecorder(), request)
	actor := ctx.Actor()
	if actor.Kind != ActorNode || actor.Realm != "members" || actor.NodeType != "user" {
		t.Fatalf("actor = %#v", actor)
	}
	principal, err := ctx.Principal()
	if err != nil || principal == nil || principal.ID != actor.NodeID {
		t.Fatalf("principal = %#v, %v", principal, err)
	}
}

func TestAuthAnonymousActor(t *testing.T) {
	s := testSite(t)
	ctx := s.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if actor := ctx.Actor(); actor.Kind != ActorAnonymous || actor.Authenticated() {
		t.Fatalf("actor = %#v", actor)
	}
	if _, err := ctx.Principal(); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("Principal error = %v", err)
	}
}

func TestAPIKeyActorAdapter(t *testing.T) {
	s := testSite(t)
	ctx := s.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	ctx.SetActor(Actor{Kind: ActorAPIKey, Scopes: []string{"content:read"}})
	actor := ctx.Actor()
	if actor.Kind != ActorAPIKey || !actor.Authenticated() || len(actor.Scopes) != 1 {
		t.Fatalf("api key actor = %#v", actor)
	}
	if _, err := ctx.Principal(); !errors.Is(err, ErrNoPrincipal) {
		t.Fatalf("API key Principal error = %v", err)
	}
}

func TestAuthRealmRejectsMismatchedSession(t *testing.T) {
	s := testSite(t)
	id, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "staff", Display: "staff", Fields: core.Fields{"name": "staff"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Core stores Realm opaquely; Web rejects a Session whose Node does not
	// match the server-side Realm mapping.
	token, err := s.Engine().CreateSession(t.Context(), "members", id)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	ctx := s.CmsCtxMaker(httptest.NewRecorder(), request)
	if actor := ctx.Actor(); actor.Kind != ActorAnonymous {
		t.Fatalf("mismatched actor = %#v", actor)
	}
}

func TestAuthRealmConfiguration(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.Auth().Register(AuthRealm{Name: "staff", NodeType: "staff"})
	})
	defaultRealm, ok := site.Auth().DefaultRealm()
	if !ok || defaultRealm.Name != "members" || defaultRealm.NodeType != "user" {
		t.Fatalf("default realm = %#v, %v", defaultRealm, ok)
	}
	staff, ok := site.Auth().Realm("staff")
	if !ok || staff.NodeType != "staff" {
		t.Fatalf("staff realm = %#v, %v", staff, ok)
	}
}

func TestAuthRealmConfigurationFailsLoudly(t *testing.T) {
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) {
			site.Auth().Register(AuthRealm{Name: "plain", NodeType: "guestbook"})
		})
	})
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) {
			site.Auth().Register(AuthRealm{Name: "members", NodeType: "staff"})
		})
	})
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) {
			site.Auth().Register(AuthRealm{Name: "staff", NodeType: "staff", Default: true})
		})
	})
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	fn()
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
func newSessionWithRole(t *testing.T, s *Site, role string) *http.Cookie {
	t.Helper()
	n := &core.Node{Display: role, Fields: core.Fields{"name": role, "role": role}}
	id, err := s.Engine().RegisterAuth(t.Context(), "user", "email", role+"@x.com", core.Fields{"password": "x"}, n)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx := &CmsCtx{BaseContext: cho.MakeBaseContext(w, req), site: s}
	if _, err := AuthSession(ctx, s.Auth().MustRealm("members"), id); err != nil {
		t.Fatal(err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookie {
			return c
		}
	}
	t.Fatal("auth cookie not set")
	return nil
}

func newSession(t *testing.T, s *Site) *http.Cookie {
	t.Helper()
	n := &core.Node{Display: "a", Fields: core.Fields{"name": "a"}}
	id, err := s.Engine().RegisterAuth(t.Context(), "user", "email", "a@x.com", core.Fields{"password": "x"}, n)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	ctx := &CmsCtx{BaseContext: cho.MakeBaseContext(w, req), site: s}
	realm := s.Auth().MustRealm("members")
	token, err := AuthSession(ctx, realm, id)
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
