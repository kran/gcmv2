package web

import (
	"github.com/kran/gcmv2/core"
)

// ── web 层 hook 事件（站点渲染/数据增强扩展点） ──

const (
	HookNodeEnrich  = "web.node_enrich"   // 节点数据增强（url 注入等）— proto: (ctx, *Node)
	HookNodeRender  = "web.node_render"   // 渲染前数据注入 — proto: (ctx, *Node, map)
	HookCandidates  = "web.candidates"    // 模板候选追加 — proto: (ctx, *Node, *List[string])
	HookRender      = "web.render"        // 页面上下文注入（Page 等）— proto: (ctx, map)
	HookServeFile   = "web.serve_file"    // 服务文件端点（/static /uploads）— 插件改路径: (ctx, *string)
	HookBeforeMount = "site.before_mount" // mount 前挂载（中间件/普通路由）— proto: (*Site) error
)

// defineWebHooks 声明 web 层事件（New 时调用 — 站点 AddHook 前必须存在）。
func defineWebHooks(svc core.Engine) {
	// 事件定义失败 = 编程错误（硬编码事件名/签名）— fail-loud panic。
	err := svc.Hooks().Define(map[string]any{
		HookNodeEnrich:  func(*CmsCtx, *core.Node) error { return nil },
		HookNodeRender:  func(*CmsCtx, *core.Node, map[string]any) error { return nil },
		HookCandidates:  func(*CmsCtx, *core.Node, *core.List[string]) error { return nil },
		HookRender:      func(*CmsCtx, map[string]any) error { return nil },
		HookServeFile:   func(*CmsCtx, *string) error { return nil },
		HookBeforeMount: func(*Site) error { return nil },
	})
	if err != nil {
		panic("web: define render hooks: " + err.Error())
	}
}
