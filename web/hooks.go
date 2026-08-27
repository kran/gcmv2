package web

import (
	"github.com/kran/gcmv2/core"
)

// ── web 层 hook 事件（站点渲染/数据增强扩展点） ──

const (
	HookNodeEnrich = "web.node_enrich" // 节点数据增强（url 注入等）— proto: (ctx, *Node)
	HookNodeRender = "web.node_render" // 渲染前数据注入 — proto: (ctx, *Node, map)
	HookCandidates = "web.candidates"  // 模板候选追加 — proto: (ctx, *Node, *[]string)
	HookRender     = "web.render"      // 页面上下文注入（Page 等）— proto: (ctx, map)
)

// DefineWebHooks 声明 web 层事件（New 时调用 — 站点 AddHook 前必须存在）。
func DefineWebHooks(svc core.Engine) error {
	err := svc.Hooks().Define(
		core.HookSpec{Name: HookNodeEnrich, Proto: func(*CmsCtx, *core.Node) error { return nil }},
		core.HookSpec{Name: HookNodeRender, Proto: func(*CmsCtx, *core.Node, map[string]any) error { return nil }},
		core.HookSpec{Name: HookCandidates, Proto: func(*CmsCtx, *core.Node, *[]string) error { return nil }},
		core.HookSpec{Name: HookRender, Proto: func(*CmsCtx, map[string]any) error { return nil }},
	)

	return err
}
