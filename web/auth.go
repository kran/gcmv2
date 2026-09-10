package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kran/cho"
	"github.com/kran/gcmv2/core"
)

const authCookie = "gcm_auth"

const (
	// HookAuthRegister runs before a Realm registration is persisted.
	// Signature: func(*CmsCtx, AuthRealm, *RegisterInput, *core.Node) error.
	HookAuthRegister = "web.auth_register"
	// HookAuthLogin runs after credentials are accepted and before a Session is created.
	// Signature: func(*CmsCtx, Actor) error.
	HookAuthLogin = "web.auth_login"
)

// RegisterInput is credential-plugin-neutral registration input. Node type is
// deliberately absent: the server-selected AuthRealm owns that decision.
type RegisterInput struct {
	Method     string         `json:"method"`
	Identifier string         `json:"identifier"`
	Secret     string         `json:"secret"`
	Display    string         `json:"display"`
	Fields     map[string]any `json:"fields"`
}

// LoginInput is credential-plugin-neutral login input.
type LoginInput struct {
	Method     string `json:"method"`
	Identifier string `json:"identifier"`
	Secret     string `json:"secret"`
}

type authBackend struct {
	eng core.Engine
}

func defineAuthHooks(svc core.Engine) {
	err := svc.Hooks().Define(map[string]any{
		HookAuthRegister: func(*CmsCtx, AuthRealm, *RegisterInput, *core.Node) error { return nil },
		HookAuthLogin:    func(*CmsCtx, Actor) error { return nil },
	})
	if err != nil {
		panic("web: define auth hooks: " + err.Error())
	}
}

// mountAuth mounts only credential-independent session operations. Credential
// plugins own Realm-specific register, login, and bind routes.
func (s *Site) mountAuth(g *cho.Cho[*CmsCtx]) {
	b := &authBackend{eng: s.engine}
	g.Group("/auth", func(ag *cho.Cho[*CmsCtx]) {
		ag.Post("/logout", b.logout)
		ag.Get("/me", b.me)
	})
}

func (c *CmsCtx) authToken() string {
	if cookie, err := c.R.Cookie(authCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	header := c.R.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}

// AuthSession creates a Realm-bound Session and writes the shared login response.
func AuthSession(ctx *CmsCtx, realm AuthRealm, nodeID int64) (string, error) {
	configured, ok := ctx.site.auth.Realm(realm.Name)
	if !ok || configured.NodeType != realm.NodeType {
		return "", fmt.Errorf("web: auth realm %q is not configured", realm.Name)
	}
	node, err := ctx.site.engine.GetNodeById(ctx.R.Context(), nodeID)
	if err != nil {
		return "", err
	}
	if node == nil || node.Type != realm.NodeType {
		return "", fmt.Errorf("web: node %d does not belong to auth realm %q", nodeID, realm.Name)
	}
	token, err := ctx.site.engine.CreateSession(ctx.R.Context(), realm.Name, nodeID)
	if err != nil {
		return "", err
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: authCookie, Value: token,
		Path: "/", HttpOnly: true, Secure: ctx.site.secureCookies, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(core.SessionTTL),
	})
	actor := Actor{Kind: ActorNode, NodeID: node.ID, NodeType: node.Type, Realm: realm.Name}
	ctx.SetActor(actor)
	ctx.principal = node
	ctx.principalLoaded = true
	_ = ctx.Json(http.StatusOK, map[string]any{"token": token, "actor": actor, "user": node})
	return token, nil
}

func (b *authBackend) logout(ctx *CmsCtx) {
	if token := ctx.authToken(); token != "" {
		_ = b.eng.DeleteSession(ctx.R.Context(), token)
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: authCookie, Value: "", Path: "/", HttpOnly: true,
		Secure: ctx.site.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *authBackend) me(ctx *CmsCtx) {
	actor := ctx.Actor()
	if actor.Kind != ActorNode {
		ctx.Error(http.StatusUnauthorized, "not logged in")
		return
	}
	principal, err := ctx.Principal()
	if err != nil {
		ctx.Error(http.StatusUnauthorized, "not logged in")
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"actor": actor, "user": principal})
}

// RequireLogin allows any authenticated Actor. Handlers that need business
// fields must call Principal and therefore explicitly require a Node Actor.
func (c *CmsCtx) RequireLogin() bool {
	if c.Actor().Authenticated() {
		return true
	}
	c.Error(http.StatusUnauthorized, "login required")
	return false
}
