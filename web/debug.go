package web

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
)

// renderError 渲染失败出口（v1 语义）:
//   - Debug:  500 错误详情页（method/path/错误/候选/数据 keys — 开发者定位）
//   - 生产:   HTML 注释（fail-loud — 访客不可见, 查源码可见病灶）
func (s *Site) renderError(ctx *CmsCtx, candidates []string, data map[string]any, err error) {
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	if !s.debug {
		// HTML 注释 — 错误细节进页面源码, 不渲染给访客
		_, _ = ctx.W.Write([]byte("<!-- render error: " + htmlCommentSafe(err.Error()) + " -->"))
		return
	}
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ctx.W.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(ctx.W, `<!DOCTYPE html><html><head><title>Render Error</title></head><body style="font-family:monospace;padding:32px;background:#1a1a1a;color:#e5e7eb;">
<h1 style="color:#f87171;">Render Error</h1>
<h3 style="color:#9ca3af;">%s %s</h3>
<pre style="background:#111;padding:16px;border-radius:8px;color:#fbbf24;overflow:auto;">%s</pre>
<h4>候选模板</h4><pre style="background:#111;padding:12px;border-radius:8px;color:#93c5fd;">%s</pre>
<h4>数据 keys</h4><pre style="background:#111;padding:12px;border-radius:8px;color:#a7f3d0;">%s</pre>
</body></html>`, ctx.R.Method, ctx.R.URL.Path, html.EscapeString(err.Error()),
		html.EscapeString(strings.Join(candidates, " → ")), html.EscapeString(strings.Join(keys, ", ")))
}

// htmlCommentSafe 错误信息进 HTML 注释安全化（防 --> 提前闭合注入）。
func htmlCommentSafe(s string) string {
	return strings.ReplaceAll(s, "--", "—")
}
