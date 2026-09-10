package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
)

// ── 内容路由 ──────────────────────────────────

// nodeHandler /node/{id|address}: 纯数字按 id，否则按 addressable capability 查询。
func (s *Site) nodeHandler(ctx *CmsCtx) {
	raw := ctx.PathValue("id")
	var n *core.Node
	var err error
	if id, e := strconv.ParseInt(raw, 10, 64); e == nil {
		n, err = s.engine.GetNodeById(ctx.R.Context(), id)
	} else {
		n, err = s.engine.GetNodeByAddress(ctx.R.Context(), raw)
	}
	if err != nil {
		slog.Error("node lookup failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	if n == nil {
		s.render404(ctx)
		return
	}
	scope, err := s.policy.Scope(ctx, PolicyView, n.Type)
	if err != nil {
		slog.Error("node policy failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	visible, err := s.engine.Query(ctx.R.Context(), core.ListQuery{
		Type: n.Type, Where: gquery.EQ(gquery.System("id"), n.ID),
		Scope: scope, Page: gquery.Page{Size: 1},
	})
	if err != nil {
		slog.Error("node policy query failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	if len(visible) == 0 {
		s.render404(ctx)
		return
	}
	n = &visible[0]
	// 节点数据增强（站点 hook — url 注入等）
	if err := s.engine.Hooks().Fire(HookNodeEnrich, ctx, n); err != nil {
		slog.Error("node enrich hook failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	data := map[string]any{"Node": n, "ID": n.ID}
	// 渲染候选（节点级联 + 站点 hook 追加）
	cands := core.NewList[string]()
	cands.Append(nodeCandidates(s.engine.Types(), n)...)
	if err := s.engine.Hooks().Fire(HookCandidates, ctx, n, cands); err != nil {
		slog.Error("candidates hook failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	ctx.Render(cands.Items(), data)
}

// render404 统一 404 出口（404.html 或纯文本）。
func (s *Site) render404(ctx *CmsCtx) {
	// buffer 先行: 渲染成功才写状态 + body（失败走纯文本, 不残留半截页面）
	var buf bytes.Buffer
	data := map[string]any{"Path": ctx.R.URL.Path}
	_ = s.engine.Hooks().Fire(HookRender, ctx, data) // 404 上下文失败不阻断 404 页
	err := s.render.Render(ctx.R.Context(), &buf, []string{"404.html"}, data)
	if err == nil {
		ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
		ctx.W.WriteHeader(http.StatusNotFound)
		_, _ = ctx.W.Write(buf.Bytes())
		return
	}
	if s.debug {
		s.renderError(ctx, []string{"404.html"}, data, err)
		return
	}
	ctx.String(http.StatusNotFound, "404 page not found")
}

// nodeCandidates 节点模板级联候选。address 由 capability 指定，仍经过
// slug kind 校验，避免候选名路径穿越。
func nodeCandidates(ts *types.Types, n *core.Node) []string {
	if n != nil && n.Type != "" {
		address := ts.Address(n.Type, n.Fields)
		if address != "" && types.ValidSlug(address) {
			return []string{"node--" + n.Type + "--" + address + ".html", "node--" + n.Type + ".html", "node.html"}
		}
		return []string{"node--" + n.Type + ".html", "node.html"}
	}
	return []string{"node.html"}
}

// apiNodes 公开记录列表: /api/nodes/{type}?sort=&page=&size=。
// 行范围由 Policy 追加；不接受 Lisp filter 和 expand。复杂查询由站点业务 API
// 或受信管理端使用 Engine.Query 构建，避免向公网暴露存储表达式。
func (s *Site) apiNodes(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if _, ok := s.engine.Types().Type(typ); !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	_, publicationEnabled := s.engine.Types().Publication(typ)
	if !publicationEnabled && !s.policy.Has(typ, PolicyList) {
		ctx.Error(http.StatusNotFound, "type is not public")
		return
	}
	page := max(int(ctx.QueryNum("page", 1)), 1)
	size := min(max(int(ctx.QueryNum("size", 20)), 1), 100)
	if strings.TrimSpace(ctx.Query("filter")) != "" || strings.TrimSpace(ctx.Query("expand")) != "" {
		ctx.Error(http.StatusBadRequest, "public filter and expand are not supported")
		return
	}
	sort, err := parseSort(ctx.Query("sort"))
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	scope, err := s.policy.Scope(ctx, PolicyList, typ)
	if err != nil {
		ctx.String(http.StatusInternalServerError, "api: policy resolution failed")
		return
	}
	q := core.ListQuery{
		Type: typ, Scope: scope, Sort: sort,
		Page: gquery.Page{Number: page, Size: size},
	}
	list, total, err := s.engine.QueryPage(ctx.R.Context(), q)
	if err != nil {
		// filter 编译错误 → 400（客户端参数）
		ctx.String(http.StatusBadRequest, "api: "+err.Error())
		return
	}
	out := map[string]any{
		"items": list, "total": total, "page": page, "size": size,
	}
	b, _ := json.Marshal(out)
	ctx.W.Header().Set("Content-Type", "application/json")
	_, _ = ctx.W.Write(b)
}
