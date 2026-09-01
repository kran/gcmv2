package web

import (
	"bytes"
	"net/http"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
)

// CmsCtx 请求上下文（渲染 + 响应方法 + 引擎访问 + 当前用户）。
type CmsCtx struct {
	*cho.BaseContext
	site       *Site
	user       *core.Node // 当前登录用户（惰性解析 — 每请求缓存）
	userLoaded bool
}

// Engine 引擎访问（handler 里查数据）。
func (c *CmsCtx) Engine() core.Engine { return c.site.engine }

// Render 按候选渲染（node--{type} 级联 → 数据注入）。
func (c *CmsCtx) Render(candidates []string, data map[string]any) {
	if c.site.render == nil {
		c.String(http.StatusInternalServerError, "render engine not ready")
		return
	}
	// 页面上下文注入（HookRender — 站点放 Page/导航等; 与自定义路由页一致）
	if err := c.site.engine.Hooks().Fire(HookRender, c, data); err != nil {
		c.String(http.StatusInternalServerError, "render hook: "+err.Error())
		return
	}
	// buffer 先行: 渲染成功才写（失败不留半截页面 + 状态码正确）
	var buf bytes.Buffer
	if err := c.site.render.Render(&buf, candidates, data); err != nil {
		c.site.renderError(c, candidates, data, err)
		return
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	_, _ = c.W.Write(buf.Bytes())
}

// Func 模板函数（站点 hook 内注册 — 等价 site.Func）。
func (c *CmsCtx) Func(name string, fn any) { c.site.Func(name, fn) }
