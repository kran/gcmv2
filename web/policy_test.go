package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

var (
	testReadActions  = []ReadAction{ReadList, ReadView, ReadSearch, ReadExport}
	testWriteActions = []WriteAction{WriteCreate, WriteUpdate, WriteDelete}
)

// 每个类型在 Open 时就定义了读写授权事件；注册前 Has 为 false。
func TestPolicyEventsDefinedPerType(t *testing.T) {
	site := testSite(t)
	hooks := site.Engine().Hooks()
	for _, typeName := range []string{"article", "guestbook", "user", "staff"} {
		for _, action := range testReadActions {
			event := ReadEvent(action, typeName)
			if !hooks.Defined(event) {
				t.Fatalf("%s is not defined", event)
			}
			if hooks.Has(event) {
				t.Fatalf("%s has handlers before registration", event)
			}
		}
		for _, action := range testWriteActions {
			event := WriteEvent(action, typeName)
			if !hooks.Defined(event) {
				t.Fatalf("%s is not defined", event)
			}
			if hooks.Has(event) {
				t.Fatalf("%s has handlers before registration", event)
			}
		}
	}
	// 站点读动作不是框架事件，首次注册时定义。
	if hooks.Defined(ReadEvent("my_content", "article")) {
		t.Fatal("site read action is predefined")
	}
	configured := testSiteConfigured(t, func(site *Site) {
		site.ReadRule("my_content", "article", func(*CmsCtx, string, *gquery.Expr) error { return nil })
	})
	if !configured.Exposes("article", "my_content") {
		t.Fatal("site read action was not registered")
	}
}

func TestDefaultReadScopeUsesPublicationCapability(t *testing.T) {
	site := testSite(t)
	publishedID, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "draft",
		Fields: core.Fields{"publication_state": "draft", "body": "hidden"},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	scope, err := site.ReadScope(ctx, ReadList, "article")
	if err != nil {
		t.Fatal(err)
	}
	items, total, err := site.Engine().QueryPage(t.Context(), core.ListQuery{
		Type: "article", Where: gquery.True(), Scope: scope,
		Page: gquery.Page{Number: 1, Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != publishedID {
		t.Fatalf("default scope = %#v, total=%d", items, total)
	}
}

func TestDefaultReadScopePerReadAction(t *testing.T) {
	site := testSite(t)
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "guestbook", Display: "note", Fields: core.Fields{"title": "note"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	}); err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	for _, action := range testReadActions {
		scope, err := site.ReadScope(ctx, action, "article")
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		_, total, err := site.Engine().QueryPage(t.Context(), core.ListQuery{
			Type: "article", Where: gquery.True(), Scope: scope,
			Page: gquery.Page{Number: 1, Size: 20},
		})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if total != 1 {
			t.Fatalf("%s exposed %d article rows, want the published one", action, total)
		}
		denied, err := site.ReadScope(ctx, action, "guestbook")
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		_, total, err = site.Engine().QueryPage(t.Context(), core.ListQuery{
			Type: "guestbook", Where: gquery.True(), Scope: denied,
			Page: gquery.Page{Number: 1, Size: 20},
		})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		if total != 0 {
			t.Fatalf("%s exposed %d guestbook rows without publication", action, total)
		}
	}
}

// 读规则收窄行范围；多个侧的条件在 AST 层合并（客户端不能削弱）。
func TestReadRuleNarrowsScope(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.ReadRule(ReadList, "article", func(ctx *CmsCtx, _ string, expr *gquery.Expr) error {
			if ctx.Actor().Kind == ActorAPIKey {
				*expr = gquery.And(*expr, gquery.EQ(gquery.Field("publication_state"), "draft"))
				return nil
			}
			*expr = gquery.And(*expr, gquery.False())
			return nil
		})
	})
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "published",
		Fields: core.Fields{"publication_state": "published", "body": "visible"},
	}); err != nil {
		t.Fatal(err)
	}
	draftID, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "draft",
		Fields: core.Fields{"publication_state": "draft", "body": "hidden"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	ctx.SetActor(Actor{Kind: ActorAPIKey, Scopes: []string{"draft:read"}})
	scope, err := site.ReadScope(ctx, ReadList, "article")
	if err != nil {
		t.Fatal(err)
	}
	items, err := site.Engine().Query(t.Context(), core.ListQuery{
		Type: "article", Where: gquery.True(), Scope: scope, Page: gquery.Page{Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != draftID {
		t.Fatalf("rule scope = %#v", items)
	}
	anonymous := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	denied, err := site.ReadScope(anonymous, ReadList, "article")
	if err != nil {
		t.Fatal(err)
	}
	items, err = site.Engine().Query(t.Context(), core.ListQuery{
		Type: "article", Where: gquery.True(), Scope: denied, Page: gquery.Page{Size: 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("anonymous rule scope = %#v", items)
	}
}

func TestReadRuleCanExposeNonPublicationType(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.ReadRule(ReadList, "guestbook", func(_ *CmsCtx, _ string, expr *gquery.Expr) error {
			*expr = gquery.And(*expr, gquery.True())
			return nil
		})
	})
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "guestbook", Display: "visible", Fields: core.Fields{"title": "visible"},
	}); err != nil {
		t.Fatal(err)
	}
	response := do(site, http.MethodGet, "/api/nodes/guestbook", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("custom public list = %d: %s", response.Code, response.Body.String())
	}
}

func TestReadScopeErrors(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.ReadRule("my_content", "article", func(*CmsCtx, string, *gquery.Expr) error {
			return Unauthorized("authentication required")
		})
		site.ReadRule(ReadView, "guestbook", func(*CmsCtx, string, *gquery.Expr) error { return nil })
	})
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	// 未注册的站点动作不静默回退到公开默认。
	if _, err := site.ReadScope(ctx, "ghost_action", "article"); err == nil {
		t.Fatal("unregistered site action resolved")
	}
	// 未知类型。
	if _, err := site.ReadScope(ctx, ReadList, "ghost"); err == nil {
		t.Fatal("unknown type resolved")
	}
	// 规则返回错误原样透传。
	if _, err := site.ReadScope(ctx, "my_content", "article"); err == nil {
		t.Fatal("rule error was swallowed")
	}
	// 注册了规则但没有产出范围 = 配置错误。
	if _, err := site.ReadScope(ctx, ReadView, "guestbook"); err == nil {
		t.Fatal("empty read scope accepted")
	}
}

func TestReadRuleRegistrationPanics(t *testing.T) {
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) {
			site.ReadRule(ReadList, "ghost", func(*CmsCtx, string, *gquery.Expr) error { return nil })
		})
	})
	assertPanics(t, func() {
		testSiteConfigured(t, func(site *Site) { site.ReadRule(ReadList, "article", nil) })
	})
	site := testSite(t)
	assertPanics(t, func() {
		site.ReadRule(ReadList, "article", func(*CmsCtx, string, *gquery.Expr) error { return nil })
	})
}

func TestWriteRuleRegistration(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.WriteRule(WriteCreate, "guestbook", func(_ *CmsCtx, _ *core.Node, allow *core.List[string]) error {
			allow.Append("title")
			return nil
		})
		site.WriteRule(WriteDelete, "guestbook", func(*CmsCtx, int64) error { return nil })
	})
	hooks := site.Engine().Hooks()
	if !hooks.Has(WriteEvent(WriteCreate, "guestbook")) {
		t.Fatal("create rule not registered")
	}
	if !hooks.Has(WriteEvent(WriteDelete, "guestbook")) {
		t.Fatal("delete rule not registered")
	}
	if hooks.Has(WriteEvent(WriteUpdate, "guestbook")) {
		t.Fatal("unregistered update action has handlers")
	}
	// 删除规则无字段列表：err == nil 即放行。
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/", nil))
	if err := hookErr(t, site, WriteEvent(WriteDelete, "guestbook"), ctx, int64(1)); err != nil {
		t.Fatalf("delete rule = %v", err)
	}
}

// hookErr 直接触发一个写规则，便于断言规则本身的返回值。
func hookErr(t *testing.T, site *Site, event string, ctx *CmsCtx, args ...any) error {
	t.Helper()
	return site.Engine().Hooks().Fire(event, append([]any{ctx}, args...)...)
}

func TestWriteRuleRegistrationPanics(t *testing.T) {
	for name, configure := range map[string]func(*Site){
		"unknown action": func(site *Site) {
			site.WriteRule("publish", "guestbook", func(*CmsCtx, int64) error { return nil })
		},
		"unknown type": func(site *Site) {
			site.WriteRule(WriteCreate, "ghost", func(*CmsCtx, *core.Node, *core.List[string]) error { return nil })
		},
		"wrong signature": func(site *Site) {
			site.WriteRule(WriteDelete, "guestbook", func(*CmsCtx, *core.Node, *core.List[string]) error { return nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			assertPanics(t, func() { testSiteConfigured(t, configure) })
		})
	}
	site := testSite(t)
	assertPanics(t, func() {
		site.WriteRule(WriteCreate, "guestbook", func(*CmsCtx, *core.Node, *core.List[string]) error { return nil })
	})
}

// 同一个 (Type, action) 对不同角色可以给出不同字段集：会员只能写 body，编辑还能写发布状态。
func TestWriteRuleVariesFieldsByActor(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.WriteRule(WriteCreate, "article", func(ctx *CmsCtx, _ *core.Node, allow *core.List[string]) error {
			member, err := ctx.Principal()
			if err != nil {
				return Unauthorized("authentication required")
			}
			allow.Append("body")
			if member.Fields.Str("role") == "editor" {
				allow.Append("publication_state")
			}
			return nil
		})
	})
	member := newSessionWithRole(t, site, "member")
	editor := newSessionWithRole(t, site, "editor")
	body := map[string]any{
		"display": "hi", "fields": map[string]any{"body": "b", "publication_state": "published"},
	}
	w := do(site, http.MethodPost, "/api/nodes/article", body, member)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("member publish = %d %s", w.Code, w.Body.String())
	}
	var failure struct {
		Details map[string]string `json:"details"`
	}
	decodeBody(t, w, &failure)
	if failure.Details["publication_state"] == "" {
		t.Fatalf("member details = %#v", failure.Details)
	}
	if _, reported := failure.Details["body"]; reported {
		t.Fatalf("member's own field reported: %#v", failure.Details)
	}
	w = do(site, http.MethodPost, "/api/nodes/article", body, editor)
	if w.Code != http.StatusCreated {
		t.Fatalf("editor publish = %d %s", w.Code, w.Body.String())
	}
	// 规则可以直接拒绝：匿名（无 Principal）→ 401。
	w = do(site, http.MethodPost, "/api/nodes/article", map[string]any{"display": "hi"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous write rule = %d %s", w.Code, w.Body.String())
	}
}

// create/update 规则一个字段都没声明 = 没有授权这个动作（403）；delete 不需要字段。
func TestWriteRuleWithoutFields(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.WriteRule(WriteCreate, "guestbook", func(*CmsCtx, *core.Node, *core.List[string]) error { return nil })
		site.WriteRule(WriteDelete, "guestbook", func(*CmsCtx, int64) error { return nil })
	})
	w := do(site, http.MethodPost, "/api/nodes/guestbook", map[string]any{"display": "hi"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("create without allowed fields = %d %s", w.Code, w.Body.String())
	}
	if code := errorCode(t, w); code != string(CodeForbidden) {
		t.Fatalf("create without allowed fields code = %q", code)
	}
	id, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "guestbook", Display: "note",
	})
	if err != nil {
		t.Fatal(err)
	}
	w = do(site, http.MethodDelete, "/api/nodes/guestbook/"+strconv.FormatInt(id, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}
}
