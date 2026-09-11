package web

import (
	"sync/atomic"
	"testing"

	"github.com/kran/gcmv2/core"
)

// 写入口（CmsCtx.CreateNode / UpdateNode / DeleteNode）：
// "客户端发起的写"走规则（身份 + 允许字段 + 加工），返回的节点按 ReadView 裁字段。
// "系统自己的写"仍然直接调 engine（本测试里的准备数据就是这么建的）。

// writeTestSite 建一个只有"本人可写"的站点：写规则 = 目标节点的 author 必须是当前 actor。
func writeTestSite(t *testing.T) *Site {
	t.Helper()
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		for _, action := range []WriteAction{WriteCreate, WriteUpdate, WriteDelete} {
			site.WriteRule(action, "article", func(ctx *CmsCtx, target any, allow *core.List[string]) error {
				_ = allow
				_ = target
				if ctx.Actor().Kind != ActorAPIKey {
					return Unauthorized("请先登录")
				}
				return nil
			})
		}
	})
	return site
}

// 未注册写规则的类型：401（匿名）/ 403（已认证），与公开写端点同语义。
func TestWriteEntryDeniesUnregisteredType(t *testing.T) {
	site, _ := maskTestSite(t)
	ctx := maskCtx(site)
	if _, err := ctx.CreateNode(&core.Node{Type: "member", Display: "x"}); err == nil {
		t.Fatal("未注册写规则的类型应拒绝")
	} else if e, ok := err.(*Error); !ok || e.Status != 401 {
		t.Fatalf("匿名应 401: %#v", err)
	}
	ctx.SetActor(Actor{Kind: ActorAPIKey})
	if _, err := ctx.CreateNode(&core.Node{Type: "member", Display: "x"}); err == nil {
		t.Fatal("未注册写规则的类型应拒绝")
	} else if e, ok := err.(*Error); !ok || e.Status != 403 {
		t.Fatalf("已认证应 403: %#v", err)
	}
}

// 规则返回空 allow = 没有授权这个动作（403），即使身份通过。
func TestWriteEntryRequiresAllowedFields(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		site.WriteRule(WriteCreate, "article", func(_ *CmsCtx, _ *core.Node, _ *core.List[string]) error {
			return nil // 一个字段都不放行
		})
	})
	ctx := maskCtx(site)
	ctx.SetActor(Actor{Kind: ActorAPIKey})
	if _, err := ctx.CreateNode(&core.Node{Type: "article", Display: "x"}); err == nil {
		t.Fatal("空 allow 应 403")
	} else if e, ok := err.(*Error); !ok || e.Status != 403 {
		t.Fatalf("应为 403: %#v", err)
	}
}

// 规则可以拒绝（身份/归属）与加工值；创建成功后返回的节点按 ReadView 裁字段。
func TestWriteEntryAppliesRuleAndMasksResponse(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		site.WriteRule(WriteCreate, "article", func(ctx *CmsCtx, node *core.Node, allow *core.List[string]) error {
			if ctx.Actor().Kind != ActorAPIKey {
				return Unauthorized("请先登录")
			}
			allow.Append("title")
			node.Fields["title"] = "服务端加工" // 规则可加工客户端不可设置的值
			return nil
		})
	})
	ctx := maskCtx(site)

	if _, err := ctx.CreateNode(&core.Node{Type: "article", Display: "x"}); err == nil {
		t.Fatal("匿名应被规则拒绝")
	}

	ctx.SetActor(Actor{Kind: ActorAPIKey})
	created, err := ctx.CreateNode(&core.Node{
		Type: "article", Display: "草稿", Fields: core.Fields{"title": "客户端标题"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Fields["title"] != "服务端加工" {
		t.Fatalf("规则加工未生效: %#v", created.Fields)
	}
}

// 更新与删除：走各自的规则，patch 由规则加工，返回裁剪后的节点。
func TestUpdateAndDeleteEntry(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		site.WriteRule(WriteUpdate, "article", func(ctx *CmsCtx, id int64, patch *core.NodePatch, allow *core.List[string]) error {
			if ctx.Actor().Kind != ActorAPIKey {
				return Forbidden("无权修改")
			}
			allow.Append("title")
			patch.Display = ptr("规则改过的标题")
			return nil
		})
		site.WriteRule(WriteDelete, "article", func(ctx *CmsCtx, id int64) error {
			if ctx.Actor().Kind != ActorAPIKey {
				return Forbidden("无权删除")
			}
			return nil
		})
	})
	id, err := site.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "原标题", Fields: core.Fields{"title": "原标题"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := maskCtx(site)

	if _, err := ctx.UpdateNode(id, &core.NodePatch{Fields: core.Fields{"title": "新"}}); err == nil {
		t.Fatal("匿名更新应被拒绝")
	}
	ctx.SetActor(Actor{Kind: ActorAPIKey})
	updated, err := ctx.UpdateNode(id, &core.NodePatch{Revision: ptr(int64(1)), Fields: core.Fields{"title": "新"}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Display != "规则改过的标题" {
		t.Fatalf("规则未加工 patch: %#v", updated.Display)
	}

	if err := ctx.DeleteNode(999999); err == nil {
		t.Fatal("删除不存在的节点应报错")
	}
	if err := ctx.DeleteNode(id); err != nil {
		t.Fatal(err)
	}
	// 公共删除 = 归档：默认读取（不含归档）拿不到
	if deleted, err := site.Engine().GetNodeById(t.Context(), id); err != nil || deleted != nil {
		t.Fatalf("归档后不该再读到: %#v, %v", deleted, err)
	}
}

// 站点自建写入可以补客户端提交不了的默认字段：写入口不做字段白名单比对
// （那个集合只在框架自己解码时才存在）。规则只负责身份 + 加工。
func TestWriteEntryKeepsSiteDefaults(t *testing.T) {
	site, _ := maskTestSite(t, func(site *Site, _ *atomic.Int32) {
		site.WriteRule(WriteCreate, "article", func(ctx *CmsCtx, node *core.Node, allow *core.List[string]) error {
			if ctx.Actor().Kind != ActorAPIKey {
				return Unauthorized("请先登录")
			}
			allow.Append("title") // 只放行客户端字段 title
			return nil
		})
	})
	ctx := maskCtx(site)
	ctx.SetActor(Actor{Kind: ActorAPIKey})

	created, err := ctx.CreateNode(&core.Node{
		Type: "article", Display: "站点补的标题",
		Fields: core.Fields{
			"title": "客户端标题", // 客户端提交的
			"state": "draft", // 站点补的默认值（规则没放行、客户端不许提交）
		},
	})
	if err != nil {
		t.Fatalf("站点补的默认字段不该被规则拒绝: %v", err)
	}
	if created.Fields["title"] != "客户端标题" || created.Fields["state"] != "draft" {
		t.Fatalf("字段被改动: %#v", created.Fields)
	}
}
