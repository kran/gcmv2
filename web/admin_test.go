package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{"POST", "/admin/query/article"},
		{"GET", "/admin/tree?type=category"},
		{"GET", "/admin/expand?node=1"},
		{"POST", "/admin/settings"},
		{"GET", "/admin/search?q=x"},
		{"GET", "/admin/integrity/relations"},
		{"GET", "/admin/merge/preview?source=1&target=2"},
		{"POST", "/admin/nodes/1/archive"},
		{"POST", "/admin/nodes/1/restore"},
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
	// 登录后 me，并统一暴露 Admin Actor。
	w = do(s, "GET", "/admin/me", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("me = %d", w.Code)
	}
	var me struct {
		Actor Actor `json:"actor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Actor.Kind != ActorAdmin {
		t.Fatalf("admin actor = %#v", me.Actor)
	}
	w = do(s, "GET", "/admin/integrity/relations", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("relation integrity = %d: %s", w.Code, w.Body.String())
	}
	// 建节点（article）
	w = do(s, "POST", "/admin/nodes?type=article", map[string]any{
		"display": "后台文章",
		"fields":  map[string]any{"body": "正文", "publication_state": "published"},
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
	w = do(s, "POST", "/admin/query/article", map[string]any{
		"where": map[string]any{"op": "eq", "field": "publication_state", "value": "published"},
		"sort":  []map[string]any{{"column": "id", "desc": true}},
		"page":  map[string]any{"number": 1, "size": 10},
	}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("query = %d: %s", w.Code, w.Body.String())
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
		"revision": n.Revision,
		"fields":   map[string]any{"publication_state": "draft"},
	}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	current, err := s.Engine().GetNodeByID(t.Context(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = do(s, "POST", "/admin/nodes/"+itoa(created.ID)+"/archive", map[string]any{"revision": current.Revision}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("archive = %d: %s", w.Code, w.Body.String())
	}
	current, err = s.Engine().GetNodeByID(t.Context(), created.ID)
	if err != nil || current.ArchivedAt == nil {
		t.Fatalf("archived node = %#v, %v", current, err)
	}
	w = do(s, "POST", "/admin/nodes/"+itoa(created.ID)+"/restore", map[string]any{"revision": current.Revision}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("restore = %d: %s", w.Code, w.Body.String())
	}
	// 永久删除
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

// adminCookie 设固定密码并登录，返回管理端会话 cookie。
func adminCookie(t *testing.T, s *Site) *http.Cookie {
	t.Helper()
	if err := NewService(s.DB()).SetPassword("cmx12345"); err != nil {
		t.Fatal(err)
	}
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "cmx12345"})
	if w.Code != http.StatusOK {
		t.Fatalf("admin login = %d: %s", w.Code, w.Body.String())
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("admin login: no cookie")
	return nil
}

// TestAdminNodesRefFilterAndExpand 后台列表对 ref 字段的两件事：
//
//	① 按任意引用字段筛选（lisp 的 in ->field [ids]，活动报名按活动筛名单就是它）
//	② 列表批量展开一层出边，前端才有显示名可渲染（ref 的值不在 fields 里）
func TestAdminNodesRefFilterAndExpand(t *testing.T) {
	s := testSiteWithTemplates(t)
	ck := adminCookie(t, s)
	var w *httptest.ResponseRecorder

	mkCategory := func(display string, parent int64) int64 {
		t.Helper()
		fields := core.Fields{"publication_state": "published"}
		if parent > 0 {
			fields["parent"] = parent
		}
		id, err := s.Engine().CreateNode(t.Context(), &core.Node{Type: "category", Display: display, Fields: fields})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	mkArticle := func(display string, category int64) int64 {
		t.Helper()
		fields := core.Fields{"publication_state": "published"}
		if category > 0 {
			fields["category"] = category
		}
		id, err := s.Engine().CreateNode(t.Context(), &core.Node{Type: "article", Display: display, Fields: fields})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	news := mkCategory("商会新闻", 0)
	sub := mkCategory("行业动态", news) // 子分类：命中父分类时它也该被算进来（前端拼子树）
	mkArticle("挂在父分类下", news)
	mkArticle("挂在子分类下", sub)
	mkArticle("没挂分类", 0)

	type listResp struct {
		Items []core.Node `json:"items"`
		Total int64       `json:"total"`
	}
	fetch := func(query string) listResp {
		t.Helper()
		path := "/admin/nodes?type=article&size=20&" + query
		w := do(s, "GET", path, nil, ck)
		if w.Code != http.StatusOK {
			t.Fatalf("list %s = %d: %s", query, w.Code, w.Body.String())
		}
		var got listResp
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// ① 精确按一个分类筛
	got := fetch("filter=" + url.QueryEscape("(in ->category ["+itoa(sub)+"])"))
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Display != "挂在子分类下" {
		t.Fatalf("按子分类筛选 = %d 条 %v", got.Total, got.Items)
	}
	// 前端把父分类展开成子树 id 列表后就是这样查的
	got = fetch("filter=" + url.QueryEscape("(in ->category ["+itoa(news)+" "+itoa(sub)+"])"))
	if got.Total != 2 {
		t.Fatalf("按子树筛选 = %d 条, want 2", got.Total)
	}

	// ② 列表必须带上展开后的引用（前端列/标题直接用 display 渲染）
	got = fetch("")
	if got.Total != 3 {
		t.Fatalf("全量 = %d 条, want 3", got.Total)
	}
	expanded := 0
	for _, n := range got.Items {
		if n.Display == "没挂分类" {
			if _, ok := n.Expand["category"]; ok {
				t.Fatal("没挂分类的节点不该有 expand.category")
			}
			continue
		}
		// 走的是 HTTP, expand 里是解码后的 map; 前端要的就是里面的 display
		ref, ok := n.Expand["category"].(map[string]any)
		if !ok || ref == nil || ref["display"] == "" {
			t.Fatalf("%s 没有展开 category: %#v", n.Display, n.Expand)
		}
		if ref["display"] != "商会新闻" && ref["display"] != "行业动态" {
			t.Fatalf("%s 展开到意外的分类: %v", n.Display, ref["display"])
		}
		expanded++
	}
	if expanded != 2 {
		t.Fatalf("展开了 %d 条, want 2", expanded)
	}

	// ③ 结构化入口（QuerySpec）必须和列表端点给同一种形状：它也展开一层出边 ——
	// 否则拿它做的界面里, 引用列只能是空的（spec 的 in/ref 跟 lisp 的 (in ->f [ids])
	// 是同一套 AST, 差别只在输出形状）。
	w = do(s, "POST", "/admin/query/article", map[string]any{
		"where": map[string]any{"op": "in", "ref": "category", "values": []any{sub}},
		"page":  map[string]any{"number": 1, "size": 20},
	}, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("结构化查询 = %d: %s", w.Code, w.Body.String())
	}
	var specResp struct {
		Items []struct {
			Display string         `json:"display"`
			Expand  map[string]any `json:"expand"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &specResp); err != nil {
		t.Fatal(err)
	}
	if specResp.Total != 1 || len(specResp.Items) != 1 || specResp.Items[0].Display != "挂在子分类下" {
		t.Fatalf("结构化查询命中 = %d 条 %v", specResp.Total, specResp.Items)
	}
	if ref, ok := specResp.Items[0].Expand["category"].(map[string]any); !ok || ref["display"] != "行业动态" {
		t.Fatalf("结构化查询没展开 category: %#v", specResp.Items[0].Expand)
	}

	// ③ 非法 filter 要 fail-loud（前端靠这个报错，而不是悄悄返回全量）
	w = do(s, "GET", "/admin/nodes?type=article&filter="+url.QueryEscape("(in ->nope [1])"), nil, ck)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法 filter = %d, want 422: %s", w.Code, w.Body.String())
	}
}

// TestAdminSearchSort 引用选择器预载用的排序：sort 只在带 type 时可用。
// 不带 type 的搜索是跨类型汇总、固定按更新时间排 —— 传了 sort 属于用错，报错而不是悄悄忽略。
func TestAdminSearchSort(t *testing.T) {
	s := testSiteWithTemplates(t)
	ck := adminCookie(t, s)

	var newest int64
	for _, display := range []string{"文章一", "文章二", "文章三"} {
		id, err := s.Engine().CreateNode(t.Context(), &core.Node{Type: "article", Display: display,
			Fields: core.Fields{"publication_state": "published"}})
		if err != nil {
			t.Fatal(err)
		}
		newest = id
	}

	type searchResp struct {
		Items []struct {
			ID      int64  `json:"id"`
			Display string `json:"display"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	fetch := func(query string) searchResp {
		t.Helper()
		w := do(s, "GET", "/admin/search?"+query, nil, ck)
		if w.Code != http.StatusOK {
			t.Fatalf("search %s = %d: %s", query, w.Code, w.Body.String())
		}
		var got searchResp
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	// 不传 sort：行为不变（默认顺序），不能因为加了排序参数就报错
	if got := fetch("type=article&size=3"); len(got.Items) != 3 {
		t.Fatalf("不带 sort = %d 条, want 3", len(got.Items))
	}
	// 预载的排序：新的在前
	got := fetch("type=article&size=3&sort=" + url.QueryEscape("-id"))
	if len(got.Items) != 3 || got.Items[0].ID != newest {
		t.Fatalf("sort=-id 首条 = %+v, want id %d", got.Items[0], newest)
	}
	// sort 不带 type：用错，fail-loud
	if w := do(s, "GET", "/admin/search?sort="+url.QueryEscape("-id"), nil, ck); w.Code != http.StatusBadRequest {
		t.Fatalf("sort 不带 type = %d, want 400: %s", w.Code, w.Body.String())
	}
	// 声明了不可排序的字段 → 编译期就拦住（不是静默不排序）
	if w := do(s, "GET", "/admin/search?type=article&sort=body", nil, ck); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sort=body = %d, want 422: %s", w.Code, w.Body.String())
	}
}
