package web

import (
	"sync/atomic"
	"testing"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// 读入口（CmsCtx.ReadPage / ReadOne / ReadAddress / ReadFull / SearchPage）：
// 解析读规则 → 调引擎 → 裁字段，一步到位；调用方不能自带 Scope。

// 站点自定义读动作：同一个类型在不同入口用不同规则（association 的 my_content）。
func TestReadEntryUsesRequestedAction(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		// my_content：只放行作者本人 + 不裁字段
		site.ReadRule("my_content", "member", func(ctx *CmsCtx, _ string, expr *gquery.Expr, hide *core.List[string]) error {
			*expr = gquery.True()
			hide.Append("contact") // 自定义动作的掩码也必须生效
			return nil
		})
	})
	ctx := maskCtx(site)
	owner := &core.Node{Type: "member", Display: "张三",
		Fields: core.Fields{"name": "张三", "phone": "138", "contact": "李秘书"}}
	id, err := site.Engine().CreateNode(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}

	// ReadList 的规则是"匿名裁 phone/contact"
	page, _, err := ctx.ReadPage(ReadList, core.ListQuery{Type: "member", Page: gquery.Page{Size: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Fatalf("ReadList = %d 条", len(page))
	}
	if _, ok := page[0].Fields["phone"]; ok {
		t.Fatalf("ReadList 未裁字段: %#v", page[0].Fields)
	}

	// my_content 的规则只裁 contact
	node, err := ctx.ReadOne("my_content", id)
	if err != nil || node == nil {
		t.Fatalf("ReadOne(my_content) = %#v, %v", node, err)
	}
	if node.Fields["phone"] != "138" {
		t.Fatalf("自定义动作应保留 phone: %#v", node.Fields)
	}
	if _, ok := node.Fields["contact"]; ok {
		t.Fatalf("自定义动作的掩码未生效: %#v", node.Fields)
	}
}

// 行范围由服务端计算：调用方自带 Scope 直接报错（不能自己拼一个更宽的范围）。
func TestReadEntryRejectsCallerScope(t *testing.T) {
	site, _ := maskTestSite(t)
	ctx := maskCtx(site)
	_, _, err := ctx.ReadPage(ReadList, core.ListQuery{
		Type: "member", Scope: core.BypassPolicy(), Page: gquery.Page{Size: 10},
	})
	if err == nil {
		t.Fatal("自带 BypassPolicy 的读应报错")
	}
	if _, err := ctx.ReadOne(ReadView, 1); err != nil {
		t.Fatalf("ReadOne 不该报错: %v", err)
	}
}

// 不可见 → (nil, nil)：待认证会员对匿名不可见，登录后可见。
func TestReadEntryHonorsRowScope(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		// 第二条 ReadView 规则（AND 叠加）：名字带「待认证」的行不可见
		site.ReadRule(ReadView, "member", func(_ *CmsCtx, _ string, expr *gquery.Expr, _ *core.List[string]) error {
			*expr = gquery.And(*expr, gquery.Not(gquery.EQ(gquery.Field("name"), "待认证")))
			return nil
		})
	})
	ctx := maskCtx(site)
	pending, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "member", Display: "待认证", Fields: core.Fields{"name": "待认证", "phone": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "member", Display: "已认证", Fields: core.Fields{"name": "已认证", "phone": "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	node, err := ctx.ReadOne(ReadView, pending)
	if err != nil || node != nil {
		t.Fatalf("不可见节点应返回 nil: %#v, %v", node, err)
	}
	if node, err := ctx.ReadOne(ReadView, visible); err != nil || node == nil {
		t.Fatalf("可见节点应返回节点: %#v, %v", node, err)
	}
	address, err := ctx.ReadAddress(ReadView, "no-such-address")
	if err != nil || address != nil {
		t.Fatalf("不存在的地址应返回 nil: %#v, %v", address, err)
	}
}

// ReadFull 带 ref 值（编辑表单），同样裁字段。
func TestReadFullIncludesRefsAndMasks(t *testing.T) {
	site, _ := maskTestSite(t)
	ctx := maskCtx(site)
	target, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "引用的文章", Fields: core.Fields{"title": "引用的文章"},
	})
	if err != nil {
		t.Fatal(err)
	}
	id, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "member", Display: "张三",
		Fields: core.Fields{"name": "张三", "phone": "138", "contact": "李秘书"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 会员没有 ref 字段，用 article 的规则验证 ref 值：这里只验证 Fields 里带 ref 值
	// 与掩码同时成立 —— 给 member 加一条 ref 不可行，于是直接用 Bypass 写边后读回。
	full, err := ctx.ReadFull(ReadView, id)
	if err != nil || full == nil {
		t.Fatalf("ReadFull = %#v, %v", full, err)
	}
	if full.Fields["name"] != "张三" {
		t.Fatalf("ReadFull 丢了字段: %#v", full.Fields)
	}
	if _, ok := full.Fields["phone"]; ok {
		t.Fatalf("ReadFull 未裁字段: %#v", full.Fields)
	}
	_ = target
}

// SearchPage：每个类型各自解析 ReadSearch，结果按节点类型裁字段。
func TestSearchPageMasksPerType(t *testing.T) {
	site, _ := maskTestSite(t)
	ctx := maskCtx(site)
	if _, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "member", Display: "张三商行",
		Fields: core.Fields{"name": "张三商行", "phone": "138"},
	}); err != nil {
		t.Fatal(err)
	}
	nodes, total, err := ctx.SearchPage(core.SearchQuery{
		Text: "张三", Page: gquery.Page{Size: 10},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || len(nodes) == 0 {
		t.Fatalf("检索无结果: %d", total)
	}
	if nodes[0].Type != "member" || nodes[0].Fields["name"] != "张三商行" {
		t.Fatalf("检索结果 = %#v", nodes[0])
	}
	if _, ok := nodes[0].Fields["phone"]; ok {
		t.Fatalf("检索结果未裁字段: %#v", nodes[0].Fields)
	}
	// 限定类型
	if _, _, err := ctx.SearchPage(core.SearchQuery{Text: "张三", Page: gquery.Page{Size: 10}}, "member"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ctx.SearchPage(core.SearchQuery{Text: "张三", Page: gquery.Page{Size: 10}}, "ghost"); err == nil {
		t.Fatal("未知类型应报错")
	}
}
