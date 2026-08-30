package web

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/types"
)

// ── 内容路由 ──────────────────────────────────

// nodeHandler /node/{id|slug}: 纯数字按 id, 否则按 slug。不存在/未发布 → 404。
func (s *Site) nodeHandler(ctx *CmsCtx) {
	raw := ctx.PathValue("id")
	var n *core.Node
	var err error
	if id, e := strconv.ParseInt(raw, 10, 64); e == nil {
		n, err = s.eng.GetNodeById(id)
	} else {
		n, err = s.eng.GetNodeBySlug(raw)
	}
	if err != nil {
		slog.Error("node lookup failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	if n == nil || n.Status != core.StatusPublished {
		s.render404(ctx)
		return
	}
	// 节点数据增强（站点 hook — url 注入等）
	if err := s.eng.Hooks().Fire(HookNodeEnrich, ctx, n); err != nil {
		slog.Error("node enrich hook failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	data := map[string]any{"Node": n, "ID": n.ID}
	// 渲染候选（节点级联 + 站点 hook 追加）
	cands := nodeCandidates(n)
	if err := s.eng.Hooks().Fire(HookCandidates, ctx, n, &cands); err != nil {
		slog.Error("candidates hook failed", "path", raw, "err", err)
		ctx.String(http.StatusInternalServerError, "500 internal server error")
		return
	}
	ctx.Render(cands, data)
}

// render404 统一 404 出口（404.html 或纯文本）。
func (s *Site) render404(ctx *CmsCtx) {
	// buffer 先行: 渲染成功才写状态 + body（失败走纯文本, 不残留半截页面）
	var buf bytes.Buffer
	data := map[string]any{"Path": ctx.R.URL.Path}
	_ = s.eng.Hooks().Fire(HookRender, ctx, data) // 404 上下文失败不阻断 404 页
	err := s.rend.Render(&buf, []string{"404.html"}, data)
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

// nodeCandidates 节点模板级联候选:
// node--{type}--{slug}.html → node--{type}.html → node.html。
// slug 白名单校验（防路径穿越 — 候选名拼进模板根）。
func nodeCandidates(n *core.Node) []string {
	if n != nil && n.Type != "" {
		if n.Slug != "" && types.ValidSlug(n.Slug) {
			return []string{"node--" + n.Type + "--" + n.Slug + ".html", "node--" + n.Type + ".html", "node.html"}
		}
		return []string{"node--" + n.Type + ".html", "node.html"}
	}
	return []string{"node.html"}
}

// apiNodes 记录 API: /api/nodes/{type}?filter=&sort=&page=&size=&expand=
// 公开只读; Lisp filter 编译错误 → 400。
func (s *Site) apiNodes(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	if typ == "" {
		ctx.String(http.StatusBadRequest, "api: type required")
		return
	}
	page := int(ctx.QueryNum("page", 1))
	size := min(int(ctx.QueryNum("size", 20)), 100)
	filter := ctx.Query("filter")
	sort := ctx.Query("sort")
	expand := ctx.Query("expand")

	f := `(= type {:typ})`
	if filter != "" {
		f = `(and (= type {:typ}) ` + filter + `)`
	}
	q := core.ListQuery{Filter: f, Sort: sort, Expand: expand, Page: page, Size: size}
	list, total, err := s.eng.QueryPage(q, map[string]any{"typ": typ})
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
