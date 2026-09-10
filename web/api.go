package web

import (
	"errors"
	"net/http"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
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

// defineNodeHooks 声明 node CRUD hook（New 装配调用）。
func defineNodeHooks(svc core.Engine) {
	err := svc.Hooks().Define(map[string]any{
		HookBeforeCreate: func(*CmsCtx, *core.Node) error { return nil },
		HookBeforeUpdate: func(*CmsCtx, int64, *core.NodePatch) error { return nil },
		HookBeforeDelete: func(*CmsCtx, int64) error { return nil },
	})
	if err != nil {
		panic("web: define node hooks: " + err.Error())
	}
}

// ── 路由 handler ──

// apiCreateNode POST /api/nodes/{type} — Fire HookBeforeCreate（深度权限）;
// 站点 hook 校验后 CreateNode。
func (s *Site) apiCreateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	var input struct {
		Display string      `json:"display"`
		Fields  core.Fields `json:"fields"`
	}
	err := decodeStrictJSON(ctx.R.Body, &input)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	node := core.Node{Type: typ, Display: input.Display, Fields: input.Fields}
	if node.Display == "" {
		ctx.Error(http.StatusBadRequest, "display required")
		return
	}
	// 权限: 无 hook 定义 = 默认拒绝（安全 — 站点必须显式放行该类型）
	if !s.engine.Hooks().HasHook(HookBeforeCreate) {
		ctx.Error(http.StatusForbidden, "create not allowed")
		return
	}
	if err := s.engine.Hooks().Fire(HookBeforeCreate, ctx, &node); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	id, err := s.engine.CreateNode(&node)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	created, err := s.engine.GetNodeById(id)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id, "node": created})
}

// apiViewNode GET /api/nodes/{type}/{id} — 公开读（可 hook 扩展）。
func (s *Site) apiViewNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	scope, err := s.policy.Scope(ctx, PolicyView, typ)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "policy resolution failed")
		return
	}
	items, err := s.engine.Query(ctx.R.Context(), core.ListQuery{
		Type: typ, Where: gquery.EQ(gquery.System("id"), id),
		Scope: scope, Page: gquery.Page{Size: 1},
	})
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if len(items) == 0 {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"node": &items[0]})
}

// apiUpdateNode PUT /api/nodes/{type}/{id} — Fire HookBeforeUpdate（归属/角色）。
func (s *Site) apiUpdateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	existing, err := s.engine.GetNodeById(id)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "internal error")
		return
	}
	if existing == nil || existing.Type != typ {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	// 差量语义: client 提交 NodePatch（全指针 — nil = 不改字段; PATCH）
	var patch core.NodePatch
	err = decodeStrictJSON(ctx.R.Body, &patch)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	// 权限: 无 hook 默认拒绝
	if !s.engine.Hooks().HasHook(HookBeforeUpdate) {
		ctx.Error(http.StatusForbidden, "update not allowed")
		return
	}
	if err := s.engine.Hooks().Fire(HookBeforeUpdate, ctx, id, &patch); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	err = s.engine.PatchNode(id, &patch)
	if errors.Is(err, core.ErrRevisionConflict) {
		ctx.Error(http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// apiDeleteNode DELETE /api/nodes/{type}/{id} — Fire HookBeforeDelete。
func (s *Site) apiDeleteNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := s.engine.GetNodeById(id)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "internal error")
		return
	}
	if existing == nil || existing.Type != typ {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	if !s.engine.Hooks().HasHook(HookBeforeDelete) {
		ctx.Error(http.StatusForbidden, "delete not allowed")
		return
	}
	if err := s.engine.Hooks().Fire(HookBeforeDelete, ctx, id); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	err = s.engine.ArchiveNode(ctx.R.Context(), id, existing.Revision)
	if errors.Is(err, core.ErrRevisionConflict) {
		ctx.Error(http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// apiUpload POST /api/upload — 前台上传（图片/头像/相册），必须登录。
func (s *Site) apiUpload(ctx *CmsCtx) {
	if !ctx.RequireLogin() {
		return
	}
	saveUpload(s.uploadsDir, ctx)
}

// apiTree GET /api/tree/{type} — tree 类型数据（行业/地区/分类/组织机构）:
// 返回嵌套树；父字段和排序来自 tree capability，可见范围来自查询 Policy。
func (s *Site) apiTree(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	scope, err := s.policy.Scope(ctx, PolicyList, typ)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "policy resolution failed")
		return
	}
	tree, err := s.engine.LoadTree(ctx.R.Context(), typ, scope)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": tree.JsonNodes()})
}

// ── mount（route 注册 — 单独 mountNodeAPI） ──

// mountNodeApi 挂载内容 node CRUD 路由（到传入 /api Group; CORS 由 cors 插件挂）。
func (s *Site) mountNodeApi(g *cho.Cho[*CmsCtx]) {
	g.Get("/nodes/{type}", s.apiNodes)
	g.Get("/nodes/{type}/{id}", s.apiViewNode)
	g.Post("/nodes/{type}", s.apiCreateNode)
	g.Put("/nodes/{type}/{id}", s.apiUpdateNode)
	g.Delete("/nodes/{type}/{id}", s.apiDeleteNode)
	// 通用: 上传 / 树数据（"我的内容"属于站点业务语义, 由站点 API 实现）
	g.Post("/upload", s.apiUpload)
	g.Get("/tree/{type}", s.apiTree)
}
