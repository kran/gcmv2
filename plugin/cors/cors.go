// Package cors CORS 中间件插件 — 经 HookBeforeMount hook 挂到站点（mount 前 — 中间件先于路由）。
//
//	app := site.New(basedir)
//	cors.Mount(app, cors.Options{})   // 配置期 — 挂中间件（HookBeforeMount fire 时生效）
package cors

import (
	"net/http"
	"strings"

	"github.com/kran/gcmv2/web"
)

// Options CORS 插件配置（站点侧负责）。
type Options struct {
	// Origins 允许来源（空 = "*"）。
	Origins []string
}

// Mount 挂 CORS 中间件（/api 设头 + OPTIONS 预检 204; 其余放行）。
func Mount(s *web.Site, opts Options) {
	origin := "*"
	if len(opts.Origins) > 0 {
		origin = strings.Join(opts.Origins, ", ")
	}
	s.Hook(web.HookBeforeMount, func(site *web.Site) error {
		site.Router().UseCtx(func(ctx *web.CmsCtx, next func()) {
			if !strings.HasPrefix(ctx.R.URL.Path, "/api/") {
				next()
				return
			}
			ctx.SetHeader("Access-Control-Allow-Origin", origin)
			ctx.SetHeader("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			ctx.SetHeader("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if ctx.R.Method == http.MethodOptions {
				ctx.W.WriteHeader(http.StatusNoContent)
				return
			}
			next()
		})
		return nil
	})
}
