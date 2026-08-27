// Package web 站点层 — 装配（Router + 渲染 + 函数）+ 内置路由 + 多站分发。
package web

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/types"
)

// Site 站点 — Engine 的使用者（装配产物）。
// 一个 Site = 一个引擎 + 一个路由器 + 模板函数表。
// 多站 = 多个 Site + HostMux 分发。
type Site struct {
	eng   core.Engine
	db    *dba.SQL
	rend  *RenderEngine
	r     *cho.Cho[*CmsCtx]
	funcs map[string]any
	debug bool
}

// SiteSpec 站点装配配置（纯数据 — 业务由调用方在 NewSite 后直接写）。
type SiteSpec struct {
	DBPath    string // SQLite 库文件路径
	Types     string // types.yaml 路径
	Templates string // 模板目录
	Static    string // 静态资源目录（空 = 跳过）
	Uploads   string // 上传目录（空 = 跳过）
	Migrate   bool   // 是否自动跑引擎迁移（默认 true）
	// Debug 开发模式: 渲染失败显示错误详情页（模板名/行号/候选/数据 keys）。
	Debug bool
	// AdminPass 管理后台固定密码（空 = 首次生成随机密码并打印一次）。
	AdminPass string
	// SQLLogger dba SQL 日志器（nil = 默认 — dba.NewLogger(slog.Default, 1s, true)）。
	SQLLogger dba.LogFunc
	// Kinds 站点自定义 kind（types.Load 之前注册 — 类型定义里用到才需要）。
	Kinds []types.Kind
}

// NewSite 站点装配: 迁移 → 建引擎 → 渲染引擎 → 路由 + 内置路由。
func NewSite(spec SiteSpec) (*Site, error) {
	// ① 存储 + 迁移
	db, err := openDB(spec.DBPath)
	if err != nil {
		return nil, err
	}
	if spec.SQLLogger != nil {
		db = db.SetLogger(spec.SQLLogger)
	}
	// ② 类型系统
	ts, err := loadTypes(spec.Types, spec.Kinds)
	if err != nil {
		return nil, err
	}
	// ③ 引擎（New 不碰 DB — 迁移可在其后）
	svc := core.New(db, ts)
	if spec.Migrate {
		if _, err := svc.MigrateUp(); err != nil {
			return nil, err
		}
	}
	if err := DefineWebHooks(svc); err != nil {
		return nil, err
	}
	if err := defineAdminHooks(svc); err != nil {
		return nil, err
	}
	if err := defineAuthHooks(svc); err != nil {
		return nil, err
	}
	// admin 账号引导（固定密码优先, 否则随机打印一次）
	if dc, err := EnsureDefaults(db); err != nil {
		return nil, err
	} else if dc != nil {
		log.Printf("web: %s: admin created: %s / %s", spec.DBPath, dc.Username, dc.Password)
		if spec.AdminPass != "" {
			if err := NewService(db).SetPassword(spec.AdminPass); err != nil {
				return nil, err
			}
			log.Printf("web: %s: admin password set to fixed", spec.DBPath)
		}
	}
	// ④ 渲染引擎
	rend := NewRenderEngine(spec.Templates, svc)
	// ⑤ Site（先建 — cho 工厂引用同一 site）
	site := &Site{eng: svc, db: db, rend: rend, funcs: map[string]any{}, debug: spec.Debug}
	r := cho.New(func(w http.ResponseWriter, r *http.Request) *CmsCtx {
		return &CmsCtx{BaseContext: cho.MakeBaseContext(w, r), site: site}
	})
	site.r = r
	// ⑥ 内置路由（含 admin）
	site.mount(spec)
	return site, nil
}

// Engine 引擎（AddHook/Query/...）。
func (s *Site) Engine() core.Engine { return s.eng }

// DB 底层数据库句柄（逃生舱 — admin 账号表等）。
func (s *Site) DB() *dba.SQL { return s.db }

// Func 注册模板函数。
func (s *Site) Func(name string, fn any) {
	s.funcs[name] = fn
	s.rend.Func(name, fn)
}

// Get/Post 自定义路由（handler 即 cho.Handler — 无返回, 错误内部处理）。
func (s *Site) Get(path string, h func(*CmsCtx))  { s.r.Get(path, h) }
func (s *Site) Post(path string, h func(*CmsCtx)) { s.r.Post(path, h) }

// Group 路由组（中间件/子组 — admin 挂载用）。
func (s *Site) Group(prefix string, fn func(*cho.Cho[*CmsCtx])) { s.r.Group(prefix, fn) }

// Handler 路由器（HostMux 用）。
func (s *Site) Handler() http.Handler { return s.r }

// SetNotFound 404 处理器（cho 转发）。
func (s *Site) SetNotFound(h func(*CmsCtx)) { s.r.SetNotFound(h) }

// openDB 打开 SQLite（路径自动建目录）。
func openDB(path string) (*dba.SQL, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return dba.Open("sqlite", path)
}

// loadTypes 加载类型定义（yaml 文件; Kinds 先注册 — 类型定义引用到才校验通过）。
func loadTypes(path string, kinds []types.Kind) (*types.Types, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ts := types.New()
	for _, k := range kinds {
		ts.RegisterKind(k)
	}
	if err := ts.Load(data); err != nil {
		return nil, err
	}
	return ts, nil
}

// CmsCtx 请求上下文（渲染 + 响应方法 + 引擎访问 + 当前用户）。
type CmsCtx struct {
	*cho.BaseContext
	site       *Site
	user       *core.Node // 当前登录用户（惰性解析 — 每请求缓存）
	userLoaded bool
}

// Engine 引擎访问（handler 里查数据）。
func (c *CmsCtx) Engine() core.Engine { return c.site.eng }

// Render 按候选渲染（node--{type} 级联 → 数据注入）。
func (c *CmsCtx) Render(candidates []string, data map[string]any) {
	if c.site.rend == nil {
		c.String(http.StatusInternalServerError, "render engine not ready")
		return
	}
	// 页面上下文注入（HookRender — 站点放 Page/导航等; 与自定义路由页一致）
	if err := c.site.eng.Hooks().Fire(HookRender, c, data); err != nil {
		c.String(http.StatusInternalServerError, "render hook: "+err.Error())
		return
	}
	// buffer 先行: 渲染成功才写（失败不留半截页面 + 状态码正确）
	var buf bytes.Buffer
	if err := c.site.rend.Render(&buf, candidates, data); err != nil {
		c.site.renderError(c, candidates, data, err)
		return
	}
	c.SetHeader("Content-Type", "text/html; charset=utf-8")
	_, _ = c.W.Write(buf.Bytes())
}

// Func 模板函数（站点 hook 内注册 — 等价 site.Func）。
func (c *CmsCtx) Func(name string, fn any) { c.site.Func(name, fn) }

// mount 绑定全部前台路由（静态/上传/API/内容）+ admin — 目录配置空则跳过。
func (s *Site) mount(spec SiteSpec) {
	// 静态资源（带 ?w=/h=/mode= 参数 = 图片裁剪）
	if spec.Static != "" {
		fs := http.StripPrefix("/static", http.FileServer(http.Dir(spec.Static)))
		s.Get("/static/*", func(ctx *CmsCtx) { serveImg(spec.Static, "/static/", ctx, fs) })
	}
	// 上传文件服务（图片裁剪同静态）
	if spec.Uploads != "" {
		fs := http.StripPrefix("/uploads", http.FileServer(http.Dir(spec.Uploads)))
		s.Get("/uploads/*", func(ctx *CmsCtx) { serveImg(spec.Uploads, "/uploads/", ctx, fs) })
	}
	// 记录 API（公开只读; Lisp filter 直通）+ 公开创建（create 规则校验）
	s.Get("/api/nodes/{type}", s.apiNodes)
	s.Post("/api/nodes/{type}", s.apiCreateNode)
	// 认证 API（注册/登录/登出/me/bind）
	s.mountAuth()
	// 内容路由 + 404 统一出口
	s.Get("/node/{id}", s.nodeHandler)
	s.SetNotFound(s.render404)
	// admin 后台（/admin 组 — 登录保护; 上传目录空 = 禁用上传）
	s.mountAdmin(spec.Uploads)
}
