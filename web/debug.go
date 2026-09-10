package web

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// renderError 渲染失败出口:
//   - Debug:  500 错误详情页（method/path/错误/候选/数据 keys — 开发者定位）
//   - 生产:   500 + HTML 注释（细节只进页面源码；状态码必须说真话 ——
//     否则模板缺失或模板内查询失败会以 200 空白页出现，监控和爬虫都发现不了）
func (s *Site) renderError(ctx *CmsCtx, candidates []string, data map[string]any, err error) {
	slog.Error("render failed", "path", ctx.R.URL.Path, "candidates", strings.Join(candidates, ","), "err", err)
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	if !s.debug {
		ctx.W.WriteHeader(http.StatusInternalServerError)
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
