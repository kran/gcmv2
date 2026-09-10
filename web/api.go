package web

import (
	"net/http"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// ── 内容 node API（纯 hook 权限 — 深度/独特校验由站点 hook 承担） ──
//
// 认证（register/login/...）在 auth.go; 此处是 node 的 CRUD。
// 权限: 读写都由按类型定义的授权事件决定（见 policy.go）：
//
//	web.write.create|update|delete.<type>  站点规则：身份/归属校验 + 声明可写字段 + 加工值
//	web.read.list|view|search|export.<type> 站点规则：收窄行范围
//
// 两边都没有“默认放行”：写未注册规则即拒绝，读未注册规则只返回已发布记录。

// ── 路由 handler ──

// apiCreateNode POST /api/nodes/{type} — 写规则（身份 + 字段 + 加工）→ CreateNode。
func (s *Site) apiCreateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Fail(NotFound("type not found"))
		return
	}
	// 授权: 该 Type 必须注册了创建规则（未注册 = 拒绝）。
	event := WriteEvent(WriteCreate, typ)
	if !s.engine.Hooks().Has(event) {
		ctx.Fail(deniedWrite(ctx, WriteCreate, typ))
		return
	}
	var input struct {
		Display string      `json:"display"`
		Fields  core.Fields `json:"fields"`
	}
	err := ctx.BindStrictJSON(&input)
	if err != nil {
		ctx.Fail(err)
		return
	}
	if input.Display == "" {
		ctx.Fail(InvalidValue("display required"))
		return
	}
	// 字段白名单只约束客户端提交的内容: 先快照字段名，规则加工后不再参与判断。
	submitted := fieldNames(input.Fields)
	node := core.Node{Type: typ, Display: input.Display, Fields: input.Fields}
	allowed := core.NewList[string]()
	if err := s.engine.Hooks().Fire(event, ctx, &node, allowed); err != nil {
		ctx.Reject(err)
		return
	}
	if allowed.Len() == 0 {
		ctx.Fail(Forbidden("create is not allowed for type %q", typ))
		return
	}
	if rejected := rejectFields(submitted, allowed.Items()); len(rejected) > 0 {
		ctx.Fail(InvalidFields(rejected))
		return
	}
	id, err := s.engine.CreateNode(ctx.R.Context(), &node)
	if err != nil {
		ctx.Fail(err)
		return
	}
	created, err := s.engine.GetNodeById(ctx.R.Context(), id)
	if err != nil {
		ctx.Fail(err)
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id, "node": created})
}

// apiViewNode GET /api/nodes/{type}/{id} — 公开读（可 hook 扩展）。
func (s *Site) apiViewNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Fail(NotFound("type not found"))
		return
	}
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	scope, err := s.ReadScope(ctx, ReadView, typ)
	if err != nil {
		ctx.Fail(err)
		return
	}
	items, err := s.engine.Query(ctx.R.Context(), core.ListQuery{
		Type:  typ,
		Where: gquery.EQ(gquery.System("id"), id),
		Scope: scope,
		Page:  gquery.Page{Size: 1},
	})
	if err != nil {
		ctx.Fail(err)
		return
	}
	if len(items) == 0 {
		ctx.Fail(NotFound("not found"))
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"node": &items[0]})
}

// apiUpdateNode PUT /api/nodes/{type}/{id} — 写规则（身份 + 字段 + 加工）→ PatchNode。
func (s *Site) apiUpdateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Fail(NotFound("type not found"))
		return
	}
	// 授权先于存在性检查: 未注册写规则的类型不能成为“这个 id 存不存在”的探测器。
	event := WriteEvent(WriteUpdate, typ)
	if !s.engine.Hooks().Has(event) {
		ctx.Fail(deniedWrite(ctx, WriteUpdate, typ))
		return
	}
	existing, err := s.engine.GetNodeById(ctx.R.Context(), id)
	if err != nil {
		ctx.Fail(err)
		return
	}
	if existing == nil || existing.Type != typ {
		ctx.Fail(NotFound("not found"))
		return
	}
	// 差量语义: client 提交 NodePatch（全指针 — nil = 不改字段; PATCH）
	var patch core.NodePatch
	err = ctx.BindStrictJSON(&patch)
	if err != nil {
		ctx.Fail(err)
		return
	}
	submitted := fieldNames(patch.Fields)
	allowed := core.NewList[string]()
	if err := s.engine.Hooks().Fire(event, ctx, id, &patch, allowed); err != nil {
		ctx.Reject(err)
		return
	}
	if allowed.Len() == 0 {
		ctx.Fail(Forbidden("update is not allowed for type %q", typ))
		return
	}
	if rejected := rejectFields(submitted, allowed.Items()); len(rejected) > 0 {
		ctx.Fail(InvalidFields(rejected))
		return
	}
	err = s.engine.PatchNode(ctx.R.Context(), id, &patch)
	if err != nil {
		ctx.Fail(err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// apiDeleteNode DELETE /api/nodes/{type}/{id} — 写规则（归属）→ ArchiveNode。
func (s *Site) apiDeleteNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Fail(NotFound("type not found"))
		return
	}
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	event := WriteEvent(WriteDelete, typ)
	if !s.engine.Hooks().Has(event) {
		ctx.Fail(deniedWrite(ctx, WriteDelete, typ))
		return
	}
	existing, err := s.engine.GetNodeById(ctx.R.Context(), id)
	if err != nil {
		ctx.Fail(err)
		return
	}
	if existing == nil || existing.Type != typ {
		ctx.Fail(NotFound("not found"))
		return
	}
	if err := s.engine.Hooks().Fire(event, ctx, id); err != nil {
		ctx.Reject(err)
		return
	}
	err = s.engine.ArchiveNode(ctx.R.Context(), id, existing.Revision)
	if err != nil {
		ctx.Fail(err)
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
		ctx.Fail(NotFound("type not found"))
		return
	}
	scope, err := s.ReadScope(ctx, ReadList, typ)
	if err != nil {
		ctx.Fail(err)
		return
	}
	tree, err := s.engine.LoadTree(ctx.R.Context(), typ, scope)
	if err != nil {
		ctx.Fail(err)
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
