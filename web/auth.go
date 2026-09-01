package web

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
	"golang.org/x/crypto/bcrypt"
)

// ── 前台用户认证（cookie + Bearer 双轨 — 同一个 token 字符串） ──
//
// 认证信息在 core（auth_methods / sessions）; web 只做 API 形态与携带方式:
//   - register/login 成功: Set-Cookie + 响应 {token, user}（web 端自动带 cookie,
//     API 客户端用响应里的 token 字段）
//   - 认证中间件: cookie 优先, Authorization: Bearer 兜底 — 查同一 sessions 表
//   - 站点模板: 前台/API 展示

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

// defineAuthHooks 声明认证事件（New 装配调用 — 站点 AddHook 前）。
func defineAuthHooks(svc core.Engine) {
	err := svc.Hooks().Define(map[string]any{
		HookAuthRegister: func(*CmsCtx, *RegisterInput, *core.Node) error { return nil },
		HookAuthLogin:    func(*CmsCtx, int64) error { return nil },
	})
	if err != nil {
		panic("web: define auth hooks: " + err.Error())
	}
}

// mountAuth 挂载通用认证路由（到传入 Group — 已带 CORS; 子组 /auth）。
// 只含方式无关的登录态操作（logout/me/bind）; register/login 由各登录插件挂载。
func (s *Site) mountAuth(g *cho.Cho[*CmsCtx]) {
	b := &authBackend{eng: s.engine}
	g.Group("/auth", func(ag *cho.Cho[*CmsCtx]) {
		ag.Post("/logout", b.logout)
		ag.Get("/me", b.me)
		ag.Post("/bind", b.bind)
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

// AuthSession 发会话（token + cookie 双轨）— 登录插件（password/wechat）登录成功后复用。
// 返回 token; 响应已写 {token, user}。
func AuthSession(ctx *CmsCtx, eng core.Engine, nodeID int64) (string, error) {
	token, err := eng.CreateSession(nodeID)
	if err != nil {
		return "", err
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: authCookie, Value: token,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(core.SessionTTL),
	})
	u, err := eng.GetNodeById(nodeID)
	if err == nil && u != nil {
		_ = ctx.Json(http.StatusOK, map[string]any{"token": token, "user": u})
	} else {
		_ = ctx.Json(http.StatusOK, map[string]any{"token": token})
	}
	return token, nil
}

// logout 登出（删 session — cookie/Bearer 同时失效）。
func (b *authBackend) logout(ctx *CmsCtx) {
	if t := ctx.authToken(); t != "" {
		_ = ctx.site.engine.DeleteSession(t)
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
	data := core.Fields{}
	if in.Method == "email" || in.Method == "phone" {
		hash, _ := bcrypt.GenerateFromPassword([]byte(in.Secret), bcrypt.DefaultCost)
		data["password"] = string(hash)
	} else {
		data["password"] = in.Secret
	}
	if err := ctx.site.engine.AddAuthMethod(in.Type, u.ID, in.Method, in.Identifier, data); err != nil {
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
	id, err := c.site.engine.ValidSession(t)
	if err != nil || id == 0 {
		return nil
	}
	u, err := c.site.engine.GetNodeById(id)
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
			if slices.Contains(roles, r) {
				return true
			}
		}
	}
	c.Error(http.StatusForbidden, "permission denied")
	return false
}
