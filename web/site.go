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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/types"
)

// Site 站点 — Engine 的使用者（装配产物）。
// 一个 Site = 一个引擎 + 一个路由器 + 模板函数表。
// 多站 = 多个 Site + HostMux 分发。
type Site struct {
	basedir       string
	engine        core.Engine
	db            *dba.SQL
	render        *Render
	router        *cho.Cho[*CmsCtx]
	auth          *AuthRegistry
	uploadsDir    string
	debug         bool      // 开发模式: 渲染错误显示详情页
	secureCookies bool      // 前台/后台认证 Cookie 是否仅通过 HTTPS 发送
	once          sync.Once // Setup 幂等 — 带锁不重复 mount 路由
	started       bool      // Setup 已执行（Handler() 检查 — 防未 Setup）

	alive     atomic.Bool // Close 之后为 false（readyz 不再报就绪）
	closeOnce sync.Once   // Close 幂等
	closeErr  error
}

// New 站点初始化（两阶段第 ① 步）。basedir 下固定路径:
//
//	gcm.sqlite  数据库（slog.Default 日志）
//	types.yaml  类型定义
//	templates/  模板
//	static/     静态（自动建）
//	uploads/    上传（自动建）
//
// New 便捷入口: 初始化失败即 panic（fail loud 已在启动期可见）。
func New(basedir string) *Site {
	site, err := Open(basedir)
	if err != nil {
		panic(err.Error())
	}
	return site
}

// Open 站点初始化（两阶段第 ① 步）。与 New 等价, 但把 db/types/迁移/元数据
// 同步的错误返回给调用方 — 进程入口可以记录日志后优雅退出, 不 panic。
func Open(basedir string) (*Site, error) {
	// ① 存储（固定 gcm.sqlite — slog.Default 日志）
	db, err := openDB(filepath.Join(basedir, "gcm.sqlite"))
	if err != nil {
		return nil, err
	}
	// ② 类型
	ts, err := loadTypes(filepath.Join(basedir, "types.yaml"))
	if err != nil {
		db.Pool().Close()
		return nil, err
	}
	// ③ 引擎（已跑内置迁移 + Schema 元数据同步）
	engine, err := core.Open(db, ts)
	if err != nil {
		db.Pool().Close()
		return nil, err
	}
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
	// ⑥ 认证 Realm 注册表与内置 hook（配置期注册，Start 后不可变）
	site.auth = newAuthRegistry(site)
	defineWebHooks(engine)
	defineAuthHooks(engine)
	defineAdminHooks(engine)
	site.definePolicyEvents()
	// ⑦ 建 router（空 — 不挂路由; 配置期 Hook 与 Router() 挂载先于 Setup 的 mount）
	site.router = cho.New(site.CmsCtxMaker)
	site.alive.Store(true)
	return site, nil
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
	// 请求体硬上限（写在一个地方，插件路由也覆盖到）；JSON 解码在 BindStrictJSON 里再收紧。
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	}
	return &CmsCtx{BaseContext: cho.MakeBaseContext(w, r), site: s}
}

// Engine 引擎（AddHook/Query/...）。
func (s *Site) Engine() core.Engine { return s.engine }

// Auth returns the Site's server-controlled authentication Realm registry.
func (s *Site) Auth() *AuthRegistry { return s.auth }

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

// SecureCookies 设置前台和后台认证 Cookie 的 Secure 属性。
// 生产 HTTPS 环境应在 Start 前启用。
func (s *Site) SecureCookies(on bool) { s.secureCookies = on }

// Handler 路由器（Start 后有效; HostMux 用）。
func (s *Site) Handler() http.Handler {
	if !s.started {
		panic("web: Handler before Start")
	}
	return s.router
}

// Close 关闭站点并释放数据库连接池。幂等 — 重复调用返回首次结果。
// 已接收的请求由 http.Server.Shutdown 负责排空, Close 只释放站点自身资源。
func (s *Site) Close() error {
	s.closeOnce.Do(func() {
		s.alive.Store(false)
		s.closeErr = s.db.Pool().Close()
	})
	return s.closeErr
}

// openDB 打开 SQLite（固定路径 + slog.Default 日志）。
func openDB(path string) (*dba.SQL, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("web: mkdir db dir: %w", err)
		}
	}
	// SQLite 连接档位（每连接生效 / WAL 是持久设置）:
	//   foreign_keys  必须开 — 级联删除等依赖外键
	//   journal_mode  WAL — 读者不阻塞写者、写者不阻塞读者；非 WAL 下并发读写直接撞锁
	//   busy_timeout  撞锁等待而不是立即 SQLITE_BUSY（dba 不做重试）
	// 档位不满足时 core.Open 会拒绝启动（见 core.verifySQLiteProfile）。
	dsn := path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := dba.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("web: open db: %w", err)
	}
	return db.SetLogger(dba.NewLogger(slog.Default(), 0, false)), nil
}

// loadTypes 加载类型定义（yaml）— 非法定义响亮报错。
func loadTypes(path string) (*types.Types, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("web: read types: %w", err)
	}
	ts := types.New()
	if err := ts.Load(data); err != nil {
		return nil, fmt.Errorf("web: load types: %w", err)
	}
	return ts, nil
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
	s.setupHealth()
	s.router.Get("/", s.homeHandler)
	s.router.Get("/node/{id}", s.nodeHandler)
	s.router.SetNotFound(s.render404)
}

// setupHealth 运维探针。
//
//	/healthz 进程存活 — 不碰数据库, 永远 200。
//	/readyz  可以接客 — 检查连接池; Close 后不再就绪。
//
// 两者不暴露版本/配置/SQLite 细节, 无认证也可安全公开。
func (s *Site) setupHealth() {
	s.router.Get("/healthz", func(ctx *CmsCtx) {
		ctx.String(http.StatusOK, "ok")
	})
	s.router.Get("/readyz", func(ctx *CmsCtx) {
		if !s.alive.Load() {
			ctx.String(http.StatusServiceUnavailable, "closing")
			return
		}
		if err := s.db.Pool().DB.PingContext(ctx.R.Context()); err != nil {
			slog.Error("readyz: database ping", "err", err)
			ctx.String(http.StatusServiceUnavailable, "database unavailable")
			return
		}
		ctx.String(http.StatusOK, "ready")
	})
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
		// 禁止浏览器 MIME 猜测；PDF/ZIP 等主动或归档内容只允许下载。
		ctx.SetHeader("X-Content-Type-Options", "nosniff")
		if prefix == "/uploads/" {
			ext := strings.ToLower(filepath.Ext(filePath))
			if ext == ".pdf" || ext == ".zip" {
				ctx.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(filePath)))
			}
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
