package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// 字段级可见性：读规则除了收窄行范围，还能声明“这个角色看不到哪些字段”。
// 范围与掩码来自同一次规则解析（CmsCtx.ReadRule 按 (action,type) 每请求一次）。

// maskTestSite 建站点: member 有敏感字段 phone/contact 与可搜索字段 name。
// 规则 = 匿名看不到 phone/contact，认证会员全看得见（Fire 次数计数返回给测试）。
// 需要额外规则的用例传 configure（必须在 Start 之前注册）。
func maskTestSite(t *testing.T, configure ...func(*Site, *atomic.Int32)) (*Site, *atomic.Int32) {
	t.Helper()
	dir := t.TempDir()
	typesYAML := `
types:
  user:
    capabilities:
      authentication: true
    fields:
      - { name: name, kind: text }
  member:
    capabilities:
      searchable: { fields: [display, name] }
    fields:
      - { name: name, kind: text }
      - { name: phone, kind: text }
      - { name: contact, kind: text }
  article:
    fields:
      - { name: title, kind: text }
`
	if err := os.WriteFile(filepath.Join(dir, "types.yaml"), []byte(typesYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := New(dir)
	site.Auth().Register(AuthRealm{
		Name: "members", NodeType: "user", AllowRegister: true, Default: true,
	})
	fires := &atomic.Int32{}
	rule := func(ctx *CmsCtx, _ string, expr *gquery.Expr, hide *core.List[string]) error {
		fires.Add(1)
		*expr = gquery.And(*expr, gquery.True())
		if ctx.Actor().Kind == ActorAnonymous {
			hide.Append("phone", "contact")
		}
		return nil
	}
	site.ReadRule(ReadList, "member", rule)
	site.ReadRule(ReadView, "member", rule)
	site.ReadRule(ReadSearch, "member", rule)
	for _, extra := range configure {
		extra(site, fires)
	}
	site.Start()
	t.Cleanup(func() { _ = site.DB().Pool().Close() })
	return site, fires
}

// 新建一个会员节点并返回 id。
func maskTestMember(t *testing.T, site *Site) int64 {
	t.Helper()
	id, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "member", Display: "张三",
		Fields: core.Fields{"name": "张三", "phone": "13800000000", "contact": "李秘书"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// maskCtx 一次性请求上下文（单测里直接调 MaskNode/MaskNodes 用）。
func maskCtx(site *Site) *CmsCtx {
	return site.CmsCtxMaker(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

// responseFields 取响应里 node.fields（也接受数组首元素）。
func responseFields(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var single struct {
		Node struct {
			Fields map[string]any `json:"fields"`
		} `json:"node"`
	}
	if err := json.Unmarshal(body, &single); err == nil && single.Node.Fields != nil {
		return single.Node.Fields
	}
	var list struct {
		Items []struct {
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("no items in %s", body)
	}
	return list.Items[0].Fields
}

// API 出口（框架自带 /api/nodes/{type}/{id} 与列表）按角色裁剪字段。
func TestFieldMaskHidesFieldsFromApiPaths(t *testing.T) {
	site, _ := maskTestSite(t)
	id := maskTestMember(t, site)
	path := "/api/nodes/member/" + strconv.FormatInt(id, 10)

	anon := responseFields(t, do(site, http.MethodGet, path, nil).Body.Bytes())
	if anon["name"] != "张三" {
		t.Fatalf("匿名应仍能看到行: %#v", anon)
	}
	for _, hidden := range []string{"phone", "contact"} {
		if _, ok := anon[hidden]; ok {
			t.Fatalf("匿名看到了 %s: %#v", hidden, anon)
		}
	}

	member := responseFields(t, do(site, http.MethodGet, path, nil, newSession(t, site)).Body.Bytes())
	if member["phone"] != "13800000000" || member["contact"] != "李秘书" {
		t.Fatalf("认证会员应看到联系方式: %#v", member)
	}

	// 列表路径同样裁剪
	listFields := responseFields(t, do(site, http.MethodGet, "/api/nodes/member", nil).Body.Bytes())
	if _, ok := listFields["phone"]; ok {
		t.Fatalf("匿名列表看到了 phone: %#v", listFields)
	}
}

// 裁剪是只读的: 原节点（可能被同一请求里别的地方持有）不会被就地修改。
func TestFieldMaskDoesNotMutateInput(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		// article 注册规则但什么都不藏 → 走“无需裁剪”路径
		site.ReadRule(ReadView, "article", func(_ *CmsCtx, _ string, expr *gquery.Expr, _ *core.List[string]) error {
			*expr = gquery.True()
			return nil
		})
	})
	ctx := maskCtx(site)
	original := &core.Node{Type: "member", Fields: core.Fields{"name": "张三", "phone": "13800000000"}}

	masked, err := MaskNode(ctx, ReadView, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := masked.Fields["phone"]; ok {
		t.Fatalf("掩码未生效: %#v", masked.Fields)
	}
	if original.Fields["phone"] != "13800000000" {
		t.Fatalf("原节点被就地修改: %#v", original.Fields)
	}
	if masked == original {
		t.Fatal("裁剪后应返回拷贝")
	}

	// 无需裁剪时零开销: 原样返回同一指针。
	plain := &core.Node{Type: "article", Fields: core.Fields{"title": "t"}}
	same, err := MaskNode(ctx, ReadView, plain)
	if err != nil {
		t.Fatal(err)
	}
	if same != plain || same.Fields["title"] != "t" {
		t.Fatalf("无隐藏字段时应原样返回: %#v", same)
	}
}

// 展开出来的节点按它自己的类型裁剪（一次展开会跨类型）。
func TestFieldMaskCoversExpandedNodes(t *testing.T) {
	site, _ := maskTestSite(t)
	ctx := maskCtx(site)
	child := &core.Node{Type: "member", Fields: core.Fields{"name": "张三", "phone": "13800000000"}}
	parent := &core.Node{
		Type: "article", Fields: core.Fields{"title": "报道"},
		Expand: map[string]any{
			"author":  child,
			"authors": []*core.Node{child},
		},
	}
	masked, err := MaskNode(ctx, ReadView, parent)
	if err != nil {
		t.Fatal(err)
	}
	gotChild, ok := masked.Expand["author"].(*core.Node)
	if !ok {
		t.Fatalf("expand.author = %#v", masked.Expand["author"])
	}
	if _, ok := gotChild.Fields["phone"]; ok {
		t.Fatalf("展开节点未裁剪: %#v", gotChild.Fields)
	}
	gotList, ok := masked.Expand["authors"].([]*core.Node)
	if !ok || len(gotList) != 1 {
		t.Fatalf("expand.authors = %#v", masked.Expand["authors"])
	}
	if _, ok := gotList[0].Fields["phone"]; ok {
		t.Fatalf("展开列表未裁剪: %#v", gotList[0].Fields)
	}
	if child.Fields["phone"] != "13800000000" {
		t.Fatalf("子节点被就地修改: %#v", child.Fields)
	}
	// 父节点自己的字段不受影响，且 expand 容器被换成拷贝
	if masked.Fields["title"] != "报道" || masked.Expand["author"] == child {
		t.Fatalf("父节点输出 = %#v", masked)
	}
}

// 多个规则叠加: hide 取并集（和范围 AND 收窄同向）。
func TestFieldMaskUnionsMultipleRules(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		site.ReadRule(ReadView, "member", func(_ *CmsCtx, _ string, _ *gquery.Expr, hide *core.List[string]) error {
			hide.Append("name")
			return nil
		})
	})
	ctx := maskCtx(site)
	node := &core.Node{Type: "member", Fields: core.Fields{"name": "张三", "phone": "1", "contact": "2"}}
	masked, err := MaskNode(ctx, ReadView, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(masked.Fields) != 0 {
		t.Fatalf("两条规则应叠加裁剪: %#v", masked.Fields)
	}
}

// 隐藏未声明字段名 → 报错（拼错不能静默放行），API 出口 500 而不是泄漏。
func TestFieldMaskRejectsUndeclaredField(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		site.ReadRule(ReadView, "member", func(_ *CmsCtx, _ string, _ *gquery.Expr, hide *core.List[string]) error {
			hide.Append("phon") // 拼错
			return nil
		})
	})
	id := maskTestMember(t, site)
	w := do(site, http.MethodGet, "/api/nodes/member/"+strconv.FormatInt(id, 10), nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("拼错字段名 = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "13800000000") {
		t.Fatalf("报错路径泄漏了字段值: %s", w.Body.String())
	}
}

// 读规则每请求每 (action, type) 只解析一次（模板会反复调用同一类型）。
func TestReadRuleResolvesOncePerRequest(t *testing.T) {
	site, fires := maskTestSite(t)
	id := maskTestMember(t, site)
	home := fmt.Sprintf(`{{ with get %d }}x{{ end }}{{ with get %d }}y{{ end }}|`+
		`{{ range list "member" 1 10 }}a{{ end }}{{ range list "member" 1 10 }}b{{ end }}|`+
		`{{ range search "张三" "" 1 10 }}c{{ end }}{{ range list "article" 1 10 }}d{{ end }}`,
		id, id)
	if err := os.WriteFile(filepath.Join(site.render.root, "home.html"), []byte(home), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := do(site, http.MethodGet, "/", nil); w.Code != http.StatusOK {
		t.Fatalf("home = %d %s", w.Code, w.Body.String())
	}
	// ReadView 一次 + ReadList 一次 + ReadSearch 一次（article 未注册规则 → 默认，不计数）
	if got := fires.Load(); got != 3 {
		t.Fatalf("规则触发 %d 次, 应为 3 次（每 (action,type) 一次）", got)
	}
	if w := do(site, http.MethodGet, "/", nil); w.Code != http.StatusOK {
		t.Fatalf("home 第二次 = %d", w.Code)
	}
	if got := fires.Load(); got != 6 {
		t.Fatalf("两个请求后触发 %d 次, 应为 6 次（缓存不跨请求）", got)
	}
}

// 换身份（SetActor）后缓存作废 —— 否则登录前后同一个 ctx 会拿到旧掩码。
func TestSetActorInvalidatesReadRules(t *testing.T) {
	site, fires := maskTestSite(t)
	ctx := maskCtx(site)
	if _, hide, err := ctx.ReadRule(ReadList, "member"); err != nil || len(hide) != 2 {
		t.Fatalf("匿名 hide = %#v, %v", hide, err)
	}
	ctx.SetActor(Actor{Kind: ActorAPIKey})
	_, hide, err := ctx.ReadRule(ReadList, "member")
	if err != nil {
		t.Fatal(err)
	}
	if len(hide) != 0 {
		t.Fatalf("换身份后 hide = %#v, 应为空", hide)
	}
	if got := fires.Load(); got != 2 {
		t.Fatalf("换身份后应重新解析（触发 2 次）, 实际 %d 次", got)
	}
}

// 渲染出口同样裁剪：模板拿不到隐藏字段。
func TestTemplateHelpersApplyFieldMask(t *testing.T) {
	site, _ := maskTestSite(t)
	id := maskTestMember(t, site)
	home := fmt.Sprintf(`phone=[{{ with get %d }}{{ index .Fields "phone" }}{{ end }}]`, id)
	if err := os.WriteFile(filepath.Join(site.render.root, "home.html"), []byte(home), 0o644); err != nil {
		t.Fatal(err)
	}
	anon := do(site, http.MethodGet, "/", nil)
	if anon.Code != http.StatusOK || anon.Body.String() != "phone=[]" {
		t.Fatalf("匿名模板输出 = %d %q", anon.Code, anon.Body.String())
	}
	member := do(site, http.MethodGet, "/", nil, newSession(t, site))
	if member.Code != http.StatusOK || member.Body.String() != "phone=[13800000000]" {
		t.Fatalf("会员模板输出 = %d %q", member.Code, member.Body.String())
	}
}

// 站点自己的输出点用 MaskNodes（框架管不到站点自建 JSON）。
func TestSiteHandlerMasksWithMaskNodes(t *testing.T) {
	site, _ := maskTestSite(t)
	maskTestMember(t, site)
	ctx := maskCtx(site)
	items, _, err := site.Engine().QueryPage(t.Context(), core.ListQuery{
		Type: "member", Scope: core.BypassPolicy(),
		Page: gquery.Page{Size: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := MaskNodes(ctx, ReadList, items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if _, ok := items[0].Fields["phone"]; ok {
		t.Fatalf("MaskNodes 未裁剪: %#v", items[0].Fields)
	}
	if items[0].Fields["name"] != "张三" {
		t.Fatalf("非敏感字段不该被裁: %#v", items[0].Fields)
	}
}

// 树结构与列表一样裁剪（TreeNode 内联了完整节点 + children）。
func TestMaskTreeHidesFields(t *testing.T) {
	site, _ := maskTestSite(t)
	ctx := maskCtx(site)
	child := &core.TreeNode{Node: core.Node{Type: "member", Fields: core.Fields{"name": "子", "phone": "2"}}}
	root := &core.TreeNode{
		Node:     core.Node{Type: "member", Fields: core.Fields{"name": "根", "phone": "1"}},
		Children: []*core.TreeNode{child},
	}
	out, err := MaskTree(ctx, ReadList, []*core.TreeNode{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[0].Fields["phone"]; ok {
		t.Fatalf("树根未裁剪: %#v", out[0].Fields)
	}
	if _, ok := out[0].Children[0].Fields["phone"]; ok {
		t.Fatalf("树子节点未裁剪: %#v", out[0].Children[0].Fields)
	}
	if root.Fields["phone"] != "1" || child.Fields["phone"] != "2" {
		t.Fatalf("原树被就地修改: %#v %#v", root.Fields, child.Fields)
	}
}
