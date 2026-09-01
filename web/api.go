package web

import (
	"net/http"

	"github.com/kran/cho"
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
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id, "node": node})
}

// apiViewNode GET /api/nodes/{type}/{id} — 公开读（可 hook 扩展）。
func (s *Site) apiViewNode(ctx *CmsCtx) {
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	n, err := s.engine.GetNodeById(id)
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
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	if _, ok := s.engine.Types().Type(typ); !ok {
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
	if !s.engine.Hooks().HasHook(HookBeforeUpdate) {
		ctx.Error(http.StatusForbidden, "update not allowed")
		return
	}
	if err := s.engine.Hooks().Fire(HookBeforeUpdate, ctx, id, &patch); err != nil {
		ctx.Error(http.StatusForbidden, err.Error())
		return
	}
	if err := s.engine.PatchNode(id, &patch); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// apiDeleteNode DELETE /api/nodes/{type}/{id} — Fire HookBeforeDelete。
func (s *Site) apiDeleteNode(ctx *CmsCtx) {
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Error(http.StatusBadRequest, "invalid id")
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
	if err := s.engine.DeleteNode(id); err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// apiUpload POST /api/upload — 前台上传（图片/头像/相册）。
// 登录策略待定（当前公开 — 可配合 hook 在创建时校验; 防滥用可加登录）。
func (s *Site) apiUpload(ctx *CmsCtx) {
	saveUpload(s.uploadsDir, ctx)
}

// apiMine GET /api/nodes/mine?type=&page=&size= — 当前用户发布的节点
// （author = ctx.User().ID; 含草稿 — 作者可见自己的）。
func (s *Site) apiMine(ctx *CmsCtx) {
	u := ctx.User()
	if u == nil {
		ctx.Error(http.StatusUnauthorized, "login required")
		return
	}
	typ := ctx.Query("type")
	page := int(ctx.QueryNum("page", 1))
	size := int(ctx.QueryNum("size", 20))
	if typ != "" {
		f := `(and (= type {:typ}) (in ->author {:uid}))`
		list, total, err := s.engine.QueryPage(core.ListQuery{Filter: f, Page: page, Size: size},
			map[string]any{"typ": typ, "uid": u.ID})
		if err != nil {
			ctx.Error(http.StatusInternalServerError, err.Error())
			return
		}
		_ = ctx.Json(http.StatusOK, map[string]any{"items": list, "total": total, "page": page, "size": size})
		return
	}
	// 全部类型（当前用户所有发布）
	list, err := s.engine.Query(core.ListQuery{
		Filter: `(in ->author {:uid})`, Size: size}, map[string]any{"uid": u.ID})
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": list, "total": len(list), "page": 1, "size": len(list)})
}

// apiTree GET /api/tree/{type} — tree 类型数据（行业/地区/分类/组织机构）:
// 返回嵌套树 [{id, slug, display, children}]。前端组树或直接渲染。
func (s *Site) apiTree(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	tree, err := s.engine.LoadTree(typ, "parent")
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
	// 通用: 上传 / 我的发布 / 树数据
	g.Post("/upload", s.apiUpload)
	g.Get("/nodes/mine", s.apiMine)
	g.Get("/tree/{type}", s.apiTree)
}
