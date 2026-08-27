package web

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
)

// ── 前台用户认证（cookie + Bearer 双轨 — 同一个 token 字符串） ──
//
// 认证信息在 core（auth_methods / sessions）; web 只做 API 形态与携带方式:
//   - register/login 成功: Set-Cookie + 响应 {token, user}（web 端自动带 cookie,
//     API 客户端用响应里的 token 字段）
//   - 认证中间件: cookie 优先, Authorization: Bearer 兜底 — 查同一 sessions 表
//   - 权限: 类型 create 规则（Lisp 表达式 — core.EvalRule）; admin 通道不受限

const authCookie = "gcm_auth"

// ── hook 事件 ──────────────────────────────────

const (
	// HookAuthRegister 注册前（事务内 — 站点敏感字段兜底）:
	// proto func(ctx *CmsCtx, in *RegisterInput, n *core.Node) error
	// 站点: 改 n.Fields（强制角色/剥离）或拒绝注册。
	HookAuthRegister = "web.auth_register"
	// HookAuthLogin 登录成功（站点记录/风控）:
	// proto func(ctx *CmsCtx, nodeID int64) error
	HookAuthLogin = "web.auth_login"
)

// RegisterInput 注册入参（hook 可见 — 站点可改 fields 前的原始输入）。
type RegisterInput struct {
	Type       string         `json:"type"`
	Method     string         `json:"method"`
	Identifier string         `json:"identifier"`
	Secret     string         `json:"secret"`
	Display    string         `json:"display"` // 公共显示文本（固有列 — 必传）
	Fields     map[string]any `json:"fields"`
}

// LoginInput 登录入参。
type LoginInput struct {
	Type       string `json:"type"`
	Method     string `json:"method"`
	Identifier string `json:"identifier"`
	Secret     string `json:"secret"`
}

// authBackend 认证 handler 组。
type authBackend struct {
	eng core.Engine
}

// defineAuthHooks 声明认证事件（NewSite 装配调用 — 站点 AddHook 前）。
func defineAuthHooks(svc core.Engine) error {
	return svc.Hooks().Define(
		core.HookSpec{Name: HookAuthRegister, Proto: func(*CmsCtx, *RegisterInput, *core.Node) error { return nil }},
		core.HookSpec{Name: HookAuthLogin, Proto: func(*CmsCtx, int64) error { return nil }},
	)
}

// mountAuth 挂载认证路由（公开）。
func (s *Site) mountAuth() {
	b := &authBackend{eng: s.eng}
	s.Group("/api/auth", func(g *cho.Cho[*CmsCtx]) {
		g.Post("/register", b.register)
		g.Post("/login", b.login)
		g.Post("/logout", b.logout)
		g.Get("/me", b.me)
		g.Post("/bind", b.bind)
	})
}

// ── token 提取（双轨） ─────────────────────────

// token 取请求携带的会话 token: cookie 优先, Bearer 兜底。
func (c *CmsCtx) authToken() string {
	if ck, err := c.R.Cookie(authCookie); err == nil && ck.Value != "" {
		return ck.Value
	}
	if h := c.R.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

// ── handlers ───────────────────────────────────

// register 注册（一个事务: 节点 + 认证方式 + 会话 — core.RegisterAuth）;
// HookAuthRegister 在落库前触发（站点改 fields/角色）。
func (b *authBackend) register(ctx *CmsCtx) {
	var in RegisterInput
	if err := ctx.BindJson(&in); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	if in.Type == "" {
		in.Type = "user"
	}
	if in.Method == "" || in.Identifier == "" {
		ctx.Error(http.StatusBadRequest, "method and identifier required")
		return
	}
	n := &core.Node{Display: in.Display, Fields: in.Fields}
	// 站点敏感字段兜底（hook 可改 fields/拒绝）
	if err := ctx.site.eng.Hooks().Fire(HookAuthRegister, ctx, &in, n); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	id, err := ctx.site.eng.RegisterAuth(in.Type, in.Method, in.Identifier, in.Secret, n)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	// 注册即登录
	b.issueSession(ctx, id)
}

// login 登录（FindAuth + 验密 → 会话）。
func (b *authBackend) login(ctx *CmsCtx) {
	var in LoginInput
	if err := ctx.BindJson(&in); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	if in.Type == "" {
		in.Type = "user"
	}
	am, err := ctx.site.eng.FindAuth(in.Type, in.Method, in.Identifier)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "internal error")
		return
	}
	if am == nil || !ctx.site.eng.VerifyPassword(am, in.Secret) {
		ctx.Error(http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := ctx.site.eng.Hooks().Fire(HookAuthLogin, ctx, am.NodeID); err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	b.issueSession(ctx, am.NodeID)
}

// issueSession 建会话 + 双轨下发（cookie + 响应 token）。
func (b *authBackend) issueSession(ctx *CmsCtx, nodeID int64) {
	token, err := ctx.site.eng.CreateSession(nodeID)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "internal error")
		return
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: authCookie, Value: token,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(core.SessionTTL),
	})
	u, err := ctx.site.eng.GetNodeById(nodeID)
	if err != nil || u == nil {
		_ = ctx.Json(http.StatusOK, map[string]any{"token": token})
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"token": token, "user": u})
}

// logout 登出（删 session — cookie/Bearer 同时失效）。
func (b *authBackend) logout(ctx *CmsCtx) {
	if t := ctx.authToken(); t != "" {
		_ = ctx.site.eng.DeleteSession(t)
	}
	http.SetCookie(ctx.W, &http.Cookie{Name: authCookie, Value: "", Path: "/", MaxAge: -1})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// me 当前用户（未登录 401）。
func (b *authBackend) me(ctx *CmsCtx) {
	u := ctx.User()
	if u == nil {
		ctx.Error(http.StatusUnauthorized, "not logged in")
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"user": u})
}

// bind 给当前用户绑定新登录方式（登录态）。
func (b *authBackend) bind(ctx *CmsCtx) {
	u := ctx.User()
	if u == nil {
		ctx.Error(http.StatusUnauthorized, "not logged in")
		return
	}
	var in RegisterInput
	if err := ctx.BindJson(&in); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	if in.Type == "" {
		in.Type = "user"
	}
	if err := ctx.site.eng.AddAuthMethod(in.Type, u.ID, in.Method, in.Identifier, in.Secret); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// ── CmsCtx 用户访问 ───────────────────────────

// User 当前登录用户节点（惰性解析 + 每请求缓存）; 未登录返回 nil。
// 双轨: cookie 或 Authorization: Bearer 同一 token。
func (c *CmsCtx) User() *core.Node {
	if c.userLoaded {
		return c.user
	}
	c.userLoaded = true
	t := c.authToken()
	if t == "" {
		return nil
	}
	id, err := c.site.eng.ValidSession(t)
	if err != nil || id == 0 {
		return nil
	}
	u, err := c.site.eng.GetNodeById(id)
	if err != nil {
		return nil
	}
	c.user = u
	return u
}

// RequireLogin 认证短路（未登录 → 401）; 通过返回 true。
func (c *CmsCtx) RequireLogin() bool {
	if c.User() != nil {
		return true
	}
	c.Error(http.StatusUnauthorized, "login required")
	return false
}

// RequireRole 角色短路（roles 任一匹配 → 通过; 否则 403）。
func (c *CmsCtx) RequireRole(roles ...string) bool {
	if u := c.User(); u != nil {
		if r, ok := u.Fields["role"].(string); ok {
			for _, want := range roles {
				if r == want {
					return true
				}
			}
		}
	}
	c.Error(http.StatusForbidden, "permission denied")
	return false
}

// ── 公开创建 API（create 规则校验） ─────────────

// apiCreateNode POST /api/nodes/{type}: 公开创建（规则允许的类型）—
// 权限: 类型 create 表达式（空 = 公开; 求值假 = 403）; admin 通道不受限。
func (s *Site) apiCreateNode(ctx *CmsCtx) {
	typ := ctx.PathValue("type")
	td, ok := s.eng.Types().Type(typ)
	if !ok {
		ctx.Error(http.StatusBadRequest, "type not found")
		return
	}
	// create 规则（空 = 公开）
	if td.Create != "" {
		if !s.allowCreate(ctx, td.Create) {
			ctx.Error(http.StatusForbidden, "permission denied")
			return
		}
	}
	var in struct {
		Slug    string         `json:"slug"`
		Status  int            `json:"status"`
		Sort    int            `json:"sort"`
		Display string         `json:"display"` // 公共显示文本（固有列 — 必传）
		Fields  map[string]any `json:"fields"`
	}
	if err := ctx.BindJson(&in); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.eng.CreateNode(&core.Node{
		Type: typ, Slug: in.Slug, Status: in.Status, Sort: in.Sort,
		Display: in.Display, Fields: in.Fields,
	})
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id})
}

// allowCreate 求值 create 规则（auth 上下文: 当前用户或 nil）。
func (s *Site) allowCreate(ctx *CmsCtx, rule string) bool {
	ctxMap := map[string]any{"auth": nil}
	if u := ctx.User(); u != nil {
		auth := map[string]any{"id": u.ID, "type": u.Type, "slug": u.Slug}
		for k, v := range u.Fields {
			auth[k] = v
		}
		ctxMap["auth"] = auth
	}
	ok, err := core.EvalRule(rule, ctxMap)
	if err != nil {
		slog.Error("create rule eval failed", "rule", rule, "err", err)
		return false
	}
	return ok
}
