// Package password provides bcrypt-backed credentials for a configured Auth Realm.
package password

import (
	"fmt"
	"net/http"

	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/web"
	"golang.org/x/crypto/bcrypt"
)

// Options configures one password credential endpoint set.
type Options struct {
	Realm           string
	Methods         []string
	MinSecretLength int
}

// Mount adds /api/auth/{realm}/register|login|bind routes.
func Mount(s *web.Site, options Options) {
	realm := s.Auth().MustRealm(options.Realm)
	if options.MinSecretLength == 0 {
		options.MinSecretLength = 8
	}
	if options.MinSecretLength < 1 {
		panic("password: MinSecretLength must be positive")
	}
	if len(options.Methods) == 0 {
		options.Methods = []string{"email", "phone"}
	}
	methods := make(map[string]bool, len(options.Methods))
	for _, method := range options.Methods {
		if method == "" || methods[method] {
			panic("password: Methods must be non-empty and unique")
		}
		methods[method] = true
	}
	b := &backend{
		eng: s.Engine(), realm: realm, methods: methods,
		minSecretLength: options.MinSecretLength,
	}
	s.Hook(web.HookBeforeMount, func(site *web.Site) error {
		base := "/api/auth/" + realm.Name
		site.Router().Post(base+"/register", b.register)
		site.Router().Post(base+"/login", b.login)
		site.Router().Post(base+"/bind", b.bind)
		return nil
	})
}

type backend struct {
	eng             core.Engine
	realm           web.AuthRealm
	methods         map[string]bool
	minSecretLength int
}

func (b *backend) register(ctx *web.CmsCtx) {
	if !b.realm.AllowRegister {
		ctx.Error(http.StatusForbidden, "registration is disabled")
		return
	}
	var input web.RegisterInput
	err := ctx.BindStrictJSON(&input)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	err = b.validateCredential(input.Method, input.Identifier, input.Secret)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	node := &core.Node{Type: b.realm.NodeType, Display: input.Display, Fields: input.Fields}
	err = ctx.Engine().Hooks().Fire(web.HookAuthRegister, ctx, b.realm, &input, node)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Secret), bcrypt.DefaultCost)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "credential hashing failed")
		return
	}
	data := core.Fields{"password": string(hash)}
	id, err := b.eng.RegisterAuth(b.realm.NodeType, input.Method, input.Identifier, data, node)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	actor := web.Actor{Kind: web.ActorNode, NodeID: id, NodeType: b.realm.NodeType, Realm: b.realm.Name}
	err = ctx.Engine().Hooks().Fire(web.HookAuthLogin, ctx, actor)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, err.Error())
		return
	}
	_, err = web.AuthSession(ctx, b.realm, id)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "session creation failed")
	}
}

func (b *backend) login(ctx *web.CmsCtx) {
	var input web.LoginInput
	err := ctx.BindStrictJSON(&input)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	if !b.methods[input.Method] {
		ctx.Error(http.StatusBadRequest, "unsupported password method")
		return
	}
	if input.Identifier == "" || input.Secret == "" {
		ctx.Error(http.StatusBadRequest, "identifier and secret required")
		return
	}
	method, err := b.eng.FindAuth(b.realm.NodeType, input.Method, input.Identifier)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "login failed")
		return
	}
	if method == nil || !verify(method, input.Secret) {
		ctx.Error(http.StatusUnauthorized, "invalid credentials")
		return
	}
	actor := web.Actor{
		Kind: web.ActorNode, NodeID: method.NodeID,
		NodeType: b.realm.NodeType, Realm: b.realm.Name,
	}
	err = ctx.Engine().Hooks().Fire(web.HookAuthLogin, ctx, actor)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "login failed")
		return
	}
	_, err = web.AuthSession(ctx, b.realm, method.NodeID)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "session creation failed")
	}
}

func (b *backend) bind(ctx *web.CmsCtx) {
	actor := ctx.Actor()
	if actor.Kind != web.ActorNode {
		ctx.Error(http.StatusUnauthorized, "login required")
		return
	}
	if actor.Realm != b.realm.Name || actor.NodeType != b.realm.NodeType {
		ctx.Error(http.StatusForbidden, "actor does not belong to this realm")
		return
	}
	var input web.LoginInput
	err := ctx.BindStrictJSON(&input)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	err = b.validateCredential(input.Method, input.Identifier, input.Secret)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Secret), bcrypt.DefaultCost)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "credential hashing failed")
		return
	}
	data := core.Fields{"password": string(hash)}
	err = b.eng.AddAuthMethod(b.realm.NodeType, actor.NodeID, input.Method, input.Identifier, data)
	if err != nil {
		ctx.Error(http.StatusBadRequest, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) validateCredential(method, identifier, secret string) error {
	if !b.methods[method] {
		return fmt.Errorf("unsupported password method")
	}
	if identifier == "" || secret == "" {
		return fmt.Errorf("identifier and secret required")
	}
	if len(secret) < b.minSecretLength {
		return fmt.Errorf("secret must be at least %d characters", b.minSecretLength)
	}
	return nil
}

func verify(method *core.AuthMethod, secret string) bool {
	hash := method.Data.Str("password")
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}
