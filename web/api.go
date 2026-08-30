package web

import (
	"net/http"
	"strconv"

	"github.com/kran/gcmv2/core"
)

// ── 内容 node API（纯 hook 权限 — 深度/独特校验由站点 hook 承担） ──
//
// 认证（register/login/...）在 auth.go; 此处是 node 的 CRUD。
// 权限: 写操作（create/update/delete）Fire 对应 hook — 站点 AddHook
// 做任意深度校验（登录、角色、归属、数据稽核...）。hook 返回 error = 拒绝。
// 读（list/view）默认公开（列表已公开; view 可选 hook 限制）。

// ── hook 事件（web 层定义 — 站点 AddHook） ──

const (
	// HookBeforeCreate 创建前（POST /api/nodes/{type}）:
	// proto func(ctx *CmsCtx, node *core.Node) error
	// 站点: 校验登录/角色/数据; 改 node（如强制 created_by）; error 拒绝。
	HookBeforeCreate = "web.before_create"
	// HookBeforeUpdate 更新前（PUT /api/nodes/{type}/{id}）:
	// proto func(ctx *CmsCtx, id int64, patch *core.NodePatch) error
	// 站点: 归属/角色校验; error 拒绝。
	HookBeforeUpdate = "web.before_update"
	// HookBeforeDelete 删除前（DELETE /api/nodes/{type}/{id}）:
	// proto func(ctx *CmsCtx, id int64) error
	HookBeforeDelete = "web.before_delete"
)

// defineNodeHooks 声明 node CRUD hook（NewSite 装配调用）。
func defineNodeHooks(svc core.Engine) error {
	return svc.Hooks().Define(
		core.HookSpec{Name: HookBeforeCreate, Proto: func(*CmsCtx, *core.Node) error { return nil }},
		core.HookSpec{Name: HookBeforeUpdate, Proto: func(*CmsCtx, int64, *core.NodePatch) error { return nil }},
		core.HookSpec{Name: HookBeforeDelete, Proto: func(*CmsCtx, int64) error { return nil }},
	)
}

// ── 路由 handler ──

// apiCreateNode POST /api/nodes/{type} — Fire HookBeforeCreate（深度权限）;
// 站点 hook 校验后 CreateNode。
func (s *Site) apiCreateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.eng.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	// 创建全程量 — core.Node 值模型（display 必填; status 默认草稿）
	var node core.Node
	if err := ctx.BindJson(&node); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	node.Type = typ // 强制（URL 定的 — 不信任 client 传 type）
	if node.Display == "" {
		ctx.Error(http.StatusBadRequest, "display required")
		return
	}
	// 权限: 无 hook 定义 = 默认拒绝（安全 — 站点必须显式放行该类型）
	if !s.eng.Hooks().HasHook(HookBeforeCreate) {
		ctx.Error(http.StatusForbidden, "create not allowed")
		return
	}
	if err := s.eng.Hooks().Fire(HookBeforeCreate, ctx, &node); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	id, err := s.eng.CreateNode(&node)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id, "node": node})
}

// apiViewNode GET /api/nodes/{type}/{id} — 公开读（可 hook 扩展）。
func (s *Site) apiViewNode(ctx *CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	n, err := s.eng.GetNodeById(id)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if n == nil || n.Status != core.StatusPublished {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"node": n})
}

// apiUpdateNode PUT /api/nodes/{type}/{id} — Fire HookBeforeUpdate（归属/角色）。
func (s *Site) apiUpdateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := s.eng.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	// 差量语义: client 提交 NodePatch（全指针 — nil = 不改字段; PATCH）
	var patch core.NodePatch
	if err := ctx.BindJson(&patch); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	// 权限: 无 hook 默认拒绝
	if !s.eng.Hooks().HasHook(HookBeforeUpdate) {
		ctx.Error(http.StatusForbidden, "update not allowed")
		return
	}
	if err := s.eng.Hooks().Fire(HookBeforeUpdate, ctx, id, &patch); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	if err := s.eng.PatchNode(id, &patch); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// apiDeleteNode DELETE /api/nodes/{type}/{id} — Fire HookBeforeDelete。
func (s *Site) apiDeleteNode(ctx *CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	if !s.eng.Hooks().HasHook(HookBeforeDelete) {
		ctx.Error(http.StatusForbidden, "delete not allowed")
		return
	}
	if err := s.eng.Hooks().Fire(HookBeforeDelete, ctx, id); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	if err := s.eng.DeleteNode(id); err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// ── mount（route 注册 — 单独 mountNodeAPI） ──

// mountNodeAPI 挂载内容 node CRUD 路由。
func (s *Site) mountNodeAPI() {
	s.Get("/api/nodes/{type}/{id}", s.apiViewNode)
	s.Post("/api/nodes/{type}", s.apiCreateNode)
	s.Put("/api/nodes/{type}/{id}", s.apiUpdateNode)
	s.Delete("/api/nodes/{type}/{id}", s.apiDeleteNode)
}
