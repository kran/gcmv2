// Package web 站点层 — 装配（Router + 渲染 + 函数）+ 内置路由 + 多站分发。
//
// 三阶段装配（解决插件时序 — 中间件先于路由）:
//
//	app := site.New(basedir)   // ① 初始化: db/types/templates/static/uploads 固定路径 + define 全部 hook + 建 router（不挂路由）
//	app.Hook(name, fn)         // ② 配置期: addhook（事件已定义 — 无时序问题）
//	app.UseCtx(mw)             //     挂中间件（CORS 等 — 先于 Start 的路由 mount）
//	app.Start()                // ③ 启动: mount 全部路由 + fire AdminMount（传认证组）→ http.Handler
package web

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/types"
)

// Site 站点 — Engine 的使用者（装配产物）。
// 一个 Site = 一个引擎 + 一个路由器 + 模板函数表。
// 多站 = 多个 Site + HostMux 分发。
type Site struct {
	basedir    string
	engine     core.Engine
	db         *dba.SQL
	render     *Render
	router     *cho.Cho[*CmsCtx]
	uploadsDir string
	debug      bool      // 开发模式: 渲染错误显示详情页
	once       sync.Once // Setup 幂等 — 带锁不重复 mount 路由
	started    bool      // Setup 已执行（Handler() 检查 — 防未 Setup）
}

// New 站点初始化（两阶段第 ① 步）。basedir 下固定路径:
//
//	gcm.sqlite  数据库（slog.Default 日志）
//	types.yaml  类型定义
//	templates/  模板
//	static/     静态（自动建）
//	uploads/    上传（自动建）
//
// New 定义全部内置 hook（Web/Node/Auth/Admin）+ 建 router（不挂路由）。
// 失败（db/types/目录）panic — fail loud。
func New(basedir string) *Site {
	// ① 存储（固定 gcm.sqlite — slog.Default 日志）
	db := openDB(filepath.Join(basedir, "gcm.sqlite"))
	// ② 类型
	ts := loadTypes(filepath.Join(basedir, "types.yaml"))
	// ③ 引擎（New 已跑内置迁移）
	engine := core.New(db, ts)
	// ④ 渲染
	render := NewRender(filepath.Join(basedir, "templates"), engine)
	// ⑤ Site（先建 — 工厂复用）
	site := &Site{
		basedir:    basedir,
		engine:     engine,
		db:         db,
		render:     render,
		uploadsDir: filepath.Join(basedir, "uploads"),
	}
	// ⑥ 定义全部内置 hook（事件先声明 — 配置期 AddHook 无时序问题）
	defineWebHooks(engine)
	defineNodeHooks(engine)
	defineAuthHooks(engine)
	defineAdminHooks(engine)
	// ⑦ 建 router（空 — 不挂路由; 配置期 Hook 与 Router() 挂载先于 Setup 的 mount）
	site.router = cho.New(site.CmsCtxMaker)
	return site
}

// Hook 配置期注册 handler（事件已在 New 定义 — 无时序问题）。
func (s *Site) Hook(name string, fn any) {
	if err := s.engine.Hooks().AddHook(name, fn); err != nil {
		panic("web: add hook: " + err.Error())
	}
}

// Start 两阶段第 ② 步 — mount 全部路由 + fire HookAdminMount（传认证组）→ http.Handler。
// Setup 挂载全部路由（两阶段第 ② 步）。幂等 — sync.Once 带锁, 重复调用安全;
// 返回 http.Handler（HostMux 多站 / 单站 Start 共用）。
func (s *Site) Setup() http.Handler {
	s.once.Do(func() {
		s.started = true
		// HookBeforeMount — 插件挂中间件/普通路由（先于内置路由 — 中间件时序）
		if err := s.engine.Hooks().Fire(HookBeforeMount, s); err != nil {
			panic("web: before mount: " + err.Error())
		}
		// 静态/uploads 文件服务
		s.setupFiles(filepath.Join(s.basedir, "static"), "/static/*", "/static/")
		s.setupFiles(s.uploadsDir, "/uploads/*", "/uploads/")
		// API（nodes + auth 通用）
		s.setupApi()
		// 内容路由 + 404
		s.setupWeb()
		// admin（建认证组 → 内置 admin 路由 → fire AdminMount 传组）
		s.setupAdmin()
	})
	return s.router
}

// Start 单站便捷 — 等价 Setup。返回 http.Handler（直接 serve）。
func (s *Site) Start() http.Handler { return s.Setup() }

// CmsCtxMaker cho 工厂（建请求 ctx）。
func (s *Site) CmsCtxMaker(w http.ResponseWriter, r *http.Request) *CmsCtx {
	return &CmsCtx{BaseContext: cho.MakeBaseContext(w, r), site: s}
}

// Engine 引擎（AddHook/Query/...）。
func (s *Site) Engine() core.Engine { return s.engine }

// DB 底层数据库句柄（逃生舱 — admin 账号表等）。
func (s *Site) DB() *dba.SQL { return s.db }

// BaseDir 站点根目录（固定路径派生 — 插件/站点自查）。
func (s *Site) BaseDir() string { return s.basedir }

// Func 注册模板函数。
func (s *Site) Func(name string, fn any) {
	s.render.Func(name, fn)
}

// Router 路由器（cho 实例 — 站点/插件挂路由/中间件: Router().Get/Post/UseCtx/Group）。
func (s *Site) Router() *cho.Cho[*CmsCtx] { return s.router }

// Debug 开发模式（配置期调 — 渲染错误显示详情页）。
func (s *Site) Debug(on bool) { s.debug = on }

// Handler 路由器（Start 后有效; HostMux 用）。
func (s *Site) Handler() http.Handler {
	if !s.started {
		panic("web: Handler before Start")
	}
	return s.router
}

// openDB 打开 SQLite（固定路径 + slog.Default 日志）— panic 失败。
func openDB(path string) *dba.SQL {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic("web: mkdir db dir: " + err.Error())
		}
	}
	// SQLite 外键默认关 — 每连接开启（级联删除 auth 等依赖 FK 生效）
	db, err := dba.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		panic("web: open db: " + err.Error())
	}
	return db.SetLogger(dba.NewLogger(slog.Default(), 0, false))
}

// loadTypes 加载类型定义（yaml; 失败 panic — fail loud）。
func loadTypes(path string) *types.Types {
	data, err := os.ReadFile(path)
	if err != nil {
		panic("web: read types: " + err.Error())
	}
	ts := types.New()
	if err := ts.Load(data); err != nil {
		panic("web: load types: " + err.Error())
	}
	return ts
}

// setupApi 内容/认证 API 模块: /api 组（节点 CRUD/tree/upload/mine/auth 通用）。
func (s *Site) setupApi() {
	s.router.Group("/api", func(g *cho.Cho[*CmsCtx]) {
		s.mountNodeApi(g)
		s.mountAuth(g)
	})
}

// setupWeb 内容渲染模块: 默认首页 + 节点路由 + 404 出口。
func (s *Site) setupWeb() {
	s.router.Get("/", s.homeHandler)
	s.router.Get("/node/{id}", s.nodeHandler)
	s.router.SetNotFound(s.render404)
}

// homeHandler 默认首页（渲染 home.html; HookRender 已注入页面上下文 — 站点
// home 区块经 hook 注入数据）。站点自定义 home 经 HookBeforeMount 挂 Router().Get("/") 覆盖。
func (s *Site) homeHandler(ctx *CmsCtx) {
	ctx.Render([]string{"home.html"}, map[string]any{})
}

// serveFiles 服务文件端点: Fire HookServeFile（插件可改路径）+ ServeFile 兜底。
func (s *Site) serveFiles(pattern, baseDir, prefix string) {
	s.router.Get(pattern, func(ctx *CmsCtx) {
		rel := strings.TrimPrefix(ctx.R.URL.Path, prefix)
		if strings.Contains(rel, "..") {
			ctx.String(http.StatusNotFound, "404 not found")
			return
		}
		filePath := filepath.Join(baseDir, filepath.FromSlash(rel))
		// Fire — 插件（imgproc 等）可改 filePath（处理）
		if err := s.engine.Hooks().Fire(HookServeFile, ctx, &filePath); err != nil {
			ctx.String(http.StatusInternalServerError, "serve file: "+err.Error())
			return
		}
		// 兜底 — 用（可能被插件改的）filePath 服务
		http.ServeFile(ctx.W, ctx.R, filePath)
	})
}

// setupFiles 建目录（不存在自动建）+ 挂文件服务端点。
func (s *Site) setupFiles(dir, pattern, prefix string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic("web: mkdir: " + err.Error())
	}
	s.serveFiles(pattern, dir, prefix)
}
