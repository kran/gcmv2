// Package password 用户名密码登录插件（email/phone + 密码）。
//
// 复用 web 的认证基础（AuthSession / RegisterInput / hook）— 方式无关部分
// 在 web（auth.go）; 本插件只做 password 方式的注册/登录（bcrypt data["password"]）。
package password

import (
	"net/http"

	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/web"
	"golang.org/x/crypto/bcrypt"
)

// Mount 挂载 password 登录路由（/api/auth/register|login — HookBeforeMount 统一挂载）。
func Mount(s *web.Site) {
	b := &backend{eng: s.Engine()}
	s.Hook(web.HookBeforeMount, func(site *web.Site) error {
		site.Router().Post("/api/auth/register", b.register)
		site.Router().Post("/api/auth/login", b.login)
		return nil
	})
}

type backend struct {
	eng core.Engine
}

// register 注册（email/phone + 密码): 建用户节点 + auth_method(data["password"]=bcrypt) + 会话。
func (b *backend) register(ctx *web.CmsCtx) {
	var in web.RegisterInput
	if err := ctx.BindJson(&in); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	if in.Type == "" {
		in.Type = "user"
	}
	if in.Method == "" || in.Identifier == "" || in.Secret == "" {
		ctx.Error(http.StatusBadRequest, "method, identifier, secret required")
		return
	}
	n := &core.Node{Display: in.Display, Fields: in.Fields}
	// 站点敏感字段兜底（hook 可改 fields/拒绝）
	if err := ctx.Engine().Hooks().Fire(web.HookAuthRegister, ctx, &in, n); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Secret), bcrypt.DefaultCost)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	id, err := b.eng.RegisterAuth(in.Type, in.Method, in.Identifier, core.Fields{"password": string(hash)}, n)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_, _ = web.AuthSession(ctx, b.eng, id) // 注册即登录
}

// login 登录（FindAuth + VerifyPassword → 会话）。
func (b *backend) login(ctx *web.CmsCtx) {
	var in web.LoginInput
	if err := ctx.BindJson(&in); err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	if in.Type == "" {
		in.Type = "user"
	}
	am, err := b.eng.FindAuth(in.Type, in.Method, in.Identifier)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	if am == nil || !b.eng.VerifyPassword(am, in.Secret) {
		ctx.Error(http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := ctx.Engine().Hooks().Fire(web.HookAuthLogin, ctx, am.NodeID); err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = web.AuthSession(ctx, b.eng, am.NodeID)
}
