// Package web admin 管理后端（web 包内 — 登录 + 节点管理 API + 引用编辑）。: 登录（accounts + bcrypt + session cookie）+
// 按 type 分组的实体管理 API + 引用编辑（ref 字段经 fields 提交, 引擎落边）。
// 单账号每库（accounts 表, 每站一库无 site 列）。
package web

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
	"github.com/spf13/pathologize"
	"golang.org/x/crypto/bcrypt"
)

//go:embed admin
var adminFS embed.FS

// uiFS 管理 UI 静态资源根 (embed 的子目录)。
var uiFS, _ = fs.Sub(adminFS, "admin")

const (
	cookieName = "gcm_admin"
	sessionTTL = 7 * 24 * time.Hour
)

// Admin 本站管理员账号。
type Admin struct {
	ID               int64      `db:"id,omitempty"`
	Username         string     `db:"username"`
	PasswordHash     string     `db:"password_hash"`
	SessionKey       string     `db:"session_key"`
	SessionExpiresAt *time.Time `db:"session_expires_at"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
}

// AdminService 账号服务 — 每站点一个实例, 绑定本站 db。
type AdminService struct {
	db *dba.SQL
}

// NewService 建账号服务。
func NewService(db *dba.SQL) *AdminService {
	return &AdminService{db: db}
}

// DefaultCreated 首次引导生成的账号信息（密码只此一次可见）。
type DefaultCreated struct {
	Username string
	Password string
}

// EnsureDefaults 为本站库生成默认管理员（admin + 随机 16 位密码）;
// 已有账号跳过。站点项目启动时调用, 密码打印一次。
func EnsureDefaults(db *dba.SQL) (*DefaultCreated, error) {
	svc := NewService(db)
	ex, err := svc.Get()
	if err != nil {
		return nil, err
	}
	if ex != nil {
		return nil, nil
	}
	password, err := randomString(16)
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	key, err := randomString(32)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if _, err := svc.db.Insert("accounts", &Admin{
		Username: "admin", PasswordHash: string(hash),
		SessionKey: key, CreatedAt: now, UpdatedAt: now,
	}).Exec(); err != nil {
		return nil, err
	}
	return &DefaultCreated{Username: "admin", Password: password}, nil
}

// SetPassword 强制设密（handler 负责先验旧密）。
func (s *AdminService) SetPassword(password string) error {
	if len(password) < 8 {
		return errors.New("admin: password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	res, err := s.db.Update("accounts", dba.H{
		"password_hash": string(hash), "session_key": "", "session_expires_at": nil,
		"updated_at": time.Now(),
	}, "1 = 1").Exec()
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errors.New("admin: no account")
	}
	return nil
}

// VerifyPassword 验密（用户名+密码同时比对; 失败信息一致不泄露用户名）。
func (s *AdminService) VerifyPassword(username, password string) bool {
	a, err := s.Get()
	if err != nil || a == nil || a.Username != username {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) == nil
}

// NewSession 登录成功: 换新 session_key 并返回（cookie 值）。
func (s *AdminService) NewSession() (string, error) {
	key, err := randomString(32)
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().Add(sessionTTL)
	res, err := s.db.Update("accounts", dba.H{
		"session_key": key, "session_expires_at": expiresAt, "updated_at": time.Now(),
	}, "1 = 1").Exec()
	if err != nil {
		return "", err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return "", errors.New("admin: no account")
	}
	return key, nil
}

// ValidSession 校验 cookie 值是否当前 session_key。
func (s *AdminService) ValidSession(key string) bool {
	if key == "" {
		return false
	}
	a, err := s.Get()
	return err == nil && a != nil && a.SessionKey == key &&
		a.SessionExpiresAt != nil && a.SessionExpiresAt.After(time.Now())
}

// InvalidateSession 立即使当前后台会话失效。
func (s *AdminService) InvalidateSession() error {
	_, err := s.db.Update("accounts", dba.H{
		"session_key": "", "session_expires_at": nil, "updated_at": time.Now(),
	}, "1 = 1").Exec()
	return err
}

// Get 取本站账号; 未找到返回 (nil, nil)。
func (s *AdminService) Get() (*Admin, error) {
	return s.db.Select("accounts", "1 = 1").FetchOne[Admin]()
}

func randomString(n int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var sb strings.Builder
	for range n {
		i, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		sb.WriteByte(chars[i.Int64()])
	}
	return sb.String(), nil
}

// backend 管理 handler 组: 账号服务 + 引擎接口 + 类型系统。
type backend struct {
	acct          *AdminService
	eng           core.Engine
	db            *dba.SQL
	uploadDir     string
	secureCookies bool
	panels        []AdminPanel // 站点面板（AdminMount 收集 — 每次 /admin/panels 请求 Fire）
}

// internal 服务器错误统一出口: 细节进日志, 响应透传（admin 是站长工具,
// fail-loud 可见性优先; 敏感 SQL 细节留在日志）。
func (b *backend) internal(ctx *CmsCtx, err error) {
	slog.Error("admin internal", "path", ctx.R.URL.Path, "err", err)
	_ = Internal("internal error").write(ctx)
}

// fail 错误出口: 结构化/Core 已知错误按契约映射（422/404/409）,
// 未预期错误只记日志并返回通用 500。
func (b *backend) fail(ctx *CmsCtx, err error) {
	ctx.Fail(err)
}

// ── UI 静态 + 上传 ────────────────────────────────

// uiFile 管理 UI 静态资源（无构建 Vue, embed 随库分发）。
// 公开访问: SPA 自行处理登录态; API 部分仍受 authed 保护。
// 无构建 = UI 随库版本变化频繁 — 禁缓存防浏览器吃旧壳。
func (b *backend) uiFile(ctx *CmsCtx) {
	ctx.SetHeader("Cache-Control", "no-cache")
	p := path.Clean(strings.TrimPrefix(ctx.R.URL.Path, "/admin/ui/"))
	if p == "." || p == "/" || p == "" {
		p = "index.html"
	}
	data, err := fs.ReadFile(uiFS, p)
	if err != nil {
		ctx.String(http.StatusNotFound, "404 not found")
		return
	}
	ctx.SetHeader("Content-Type", mime.TypeByExtension(filepath.Ext(p)))
	_, _ = ctx.W.Write(data)
}

// uploadMIMEs 同时约束扩展名和文件头识别结果。svg/html 刻意排除（可执行内容,
// 同源服务 = 存储型 XSS）。
var uploadMIMEs = map[string]map[string]bool{
	".jpg":  {"image/jpeg": true},
	".jpeg": {"image/jpeg": true},
	".png":  {"image/png": true},
	".gif":  {"image/gif": true},
	".webp": {"image/webp": true},
	".ico":  {"image/x-icon": true, "image/vnd.microsoft.icon": true},
	".pdf":  {"application/pdf": true},
	".zip":  {"application/zip": true},
	".mp4":  {"video/mp4": true},
	".webm": {"video/webm": true, "audio/webm": true},
	".mp3":  {"audio/mpeg": true},
	".wav":  {"audio/wave": true, "audio/wav": true, "audio/x-wav": true},
}

// upload 处理 multipart 上传: 大小上限 → 扩展名白名单 → 随机文件名 → 落 uploads。
func (b *backend) upload(ctx *CmsCtx) {
	saveUpload(b.uploadDir, ctx)
}

// saveUpload 处理 multipart 上传: 大小上限 → 扩展名白名单 → 随机文件名 →
// 落 uploads。admin 后台上传与前台 API 上传共用。
func saveUpload(uploadDir string, ctx *CmsCtx) {
	if uploadDir == "" {
		ctx.Fail(BadRequest("uploads disabled"))
		return
	}
	maxBytes := int64(8 << 20) // 8MB 硬上限
	ctx.R.Body = http.MaxBytesReader(ctx.W, ctx.R.Body, maxBytes)
	if err := ctx.R.ParseMultipartForm(maxBytes); err != nil {
		ctx.Fail(Errorf(http.StatusRequestEntityTooLarge, CodeUploadInvalid, "file too large (max 8MB)"))
		return
	}
	f, fh, err := ctx.R.FormFile("file")
	if err != nil {
		ctx.Fail(Errorf(http.StatusBadRequest, CodeUploadInvalid, "missing file field 'file'"))
		return
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	allowedMIMEs, ok := uploadMIMEs[ext]
	if !ok {
		ctx.Fail(Errorf(http.StatusUnprocessableEntity, CodeUploadInvalid, "file type not allowed: %s", ext))
		return
	}
	header := make([]byte, 512)
	n, readErr := io.ReadFull(f, header)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		ctx.Fail(BadRequest("cannot read uploaded file"))
		return
	}
	contentType := http.DetectContentType(header[:n])
	if !allowedMIMEs[contentType] {
		ctx.Fail(Errorf(http.StatusUnprocessableEntity, CodeUploadInvalid, "file content does not match extension"))
		return
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		ctx.Fail(BadRequest("cannot read uploaded file"))
		return
	}
	base := strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename))
	base = pathologize.Clean(base)
	rand4 := make([]byte, 4)
	if _, err := rand.Read(rand4); err != nil {
		ctx.Fail(Internal("upload failed"))
		return
	}
	name := fmt.Sprintf("%d-%s-%s%s", time.Now().Unix(), hex.EncodeToString(rand4), base, ext)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		ctx.Fail(Internal("upload failed"))
		return
	}
	dst, err := os.OpenFile(filepath.Join(uploadDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		ctx.Fail(Internal("upload failed"))
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		_ = dst.Close()
		_ = os.Remove(dst.Name())
		ctx.Fail(Internal("upload failed"))
		return
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(dst.Name())
		ctx.Fail(Internal("upload failed"))
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"name": name, "path": "/uploads/" + name})
}

// AdminPanel 一个后台面板（站点专门管理: 菜单项 + 组件入口）。
type AdminPanel struct {
	Path  string `json:"path"`  // 组内 API 前缀, 如 "/guestbook"
	Title string `json:"title"` // 后台菜单名
	Vue   string `json:"vue"`   // 面板组件完整 URL（认证路由, 站点自己挂）
}

// HookAdminPanel 后台面板菜单事件（/admin/panels 每次请求 Fire — 响应式）:
// 原型 func(ctx *CmsCtx, panels *core.List[AdminPanel]) error — 插件只返回菜单数据。
const HookAdminPanel = "admin.panel"

// HookAdminMount 后台受保护端点挂载事件（Start 时 Fire 一次 — 传 admin 认证组）:
// 原型 func(g *cho.Cho[*CmsCtx]) error — 插件拿组挂受保护端点（替代 Site.Admin）。
const HookAdminMount = "admin.mount"

// HookAdminMount 挂载 /admin 受保护端点事件（Setup 时 Fire 一次 — 传认证组）;
// 内置 admin 路由（login/ui/upload/nodes/settings...）在此挂载。
// uploadDir 用站点 uploadsDir（可为空 = 禁用上传）; /uploads/* 服务由装配层挂载。
func defineAdminHooks(svc core.Engine) {
	err := svc.Hooks().Define(map[string]any{
		HookAdminPanel: func(*CmsCtx, *core.List[AdminPanel]) error { return nil },
		HookAdminMount: func(*cho.Cho[*CmsCtx]) error { return nil },
	})
	if err != nil {
		panic("web: define admin hooks: " + err.Error())
	}
}

// setupAdmin 后台模块: 账号引导 + /admin 组（建认证组 → fire HookAdminMount 传组）。
func (s *Site) setupAdmin() {
	dc, err := EnsureDefaults(s.DB())
	if err != nil {
		panic("web: ensure admin defaults: " + err.Error())
	}
	if dc != nil {
		slog.Info("admin created", "username", dc.Username, "password", dc.Password)
	}
	s.mountAdmin()
}

func (s *Site) mountAdmin() {
	b := &backend{
		acct: NewService(s.DB()), eng: s.engine, db: s.DB(),
		uploadDir: s.uploadsDir, secureCookies: s.secureCookies,
	}

	// /admin 组: 仅 login/ui 公开，其余端点要求管理员认证。
	s.router.Group("/admin", func(g *cho.Cho[*CmsCtx]) {
		g.Post("/login", b.login)
		g.Get("/ui/*", b.uiFile)
		g.Group("", func(authed *cho.Cho[*CmsCtx]) {
			authed.UseCtx(b.requireAuth)
			authed.Post("/logout", b.logout)
			authed.Post("/upload", b.upload)
			authed.Get("/panels", b.listPanels)
			authed.Get("/me", b.me)
			authed.Get("/types", b.types)
			authed.Get("/nodes", b.listNodes)
			authed.Post("/query/{type}", b.queryNodes)
			authed.Post("/nodes", b.createNode)
			authed.Get("/nodes/{id}", b.getNode)
			authed.Put("/nodes/{id}", b.updateNode)
			authed.Delete("/nodes/{id}", b.deleteNode)
			authed.Post("/nodes/{id}/archive", b.archiveNode)
			authed.Post("/nodes/{id}/restore", b.restoreNode)
			authed.Get("/search", b.search)
			authed.Get("/tree", b.tree)
			authed.Get("/inbound", b.inbound)
			authed.Get("/expand", b.expand)
			authed.Post("/password", b.changePassword)
			authed.Get("/settings", b.listSettings)
			authed.Post("/settings", b.setSetting)
			authed.Delete("/settings/{key}", b.deleteSetting)
			authed.Post("/search/rebuild", b.rebuildSearch)
			authed.Get("/integrity/relations", b.relationIntegrity)
			authed.Get("/merge/preview", b.mergePreview)
			// fire AdminMount（传 authed 组 — 插件挂受保护端点; 组件已建）
			if err := s.engine.Hooks().Fire(HookAdminMount, authed); err != nil {
				panic("web: fire admin mount: " + err.Error())
			}
		})
	})
}

// ── 认证 ─────────────────────────────────────────

func (b *backend) login(ctx *CmsCtx) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := ctx.BindStrictJSON(&in); err != nil {
		b.fail(ctx, err)
		return
	}
	if !b.acct.VerifyPassword(in.Username, in.Password) {
		ctx.Fail(Unauthorized("invalid credentials"))
		return
	}
	key, err := b.acct.NewSession()
	if err != nil {
		b.internal(ctx, err)
		return
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: cookieName, Value: key,
		Path: "/", HttpOnly: true, Secure: b.secureCookies, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(sessionTTL),
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) logout(ctx *CmsCtx) {
	if err := b.acct.InvalidateSession(); err != nil {
		b.internal(ctx, err)
		return
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: b.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// listPanels 站点面板列表 — 每次请求 Fire(AdminPanel)（hook 响应式查询）。
func (b *backend) listPanels(ctx *CmsCtx) {
	panels := core.NewList[AdminPanel]()
	if err := b.eng.Hooks().Fire(HookAdminPanel, ctx, panels); err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": panels.Items()})
}

// requireAuth 会话校验中间件（cho 类型化中间件: 校验失败短路）。
func (b *backend) requireAuth(ctx *CmsCtx, next func()) {
	cookie, err := ctx.R.Cookie(cookieName)
	if err != nil || !b.acct.ValidSession(cookie.Value) {
		ctx.Fail(Unauthorized("unauthorized"))
		return
	}
	ctx.SetActor(Actor{Kind: ActorAdmin, Scopes: []string{"admin"}})
	next()
}

func (b *backend) me(ctx *CmsCtx) {
	_ = ctx.Json(http.StatusOK, map[string]any{"username": "admin", "actor": ctx.Actor()})
}

// ── 类型定义 ─────────────────────────────────────

// types 类型定义（admin UI 动态表单渲染用）。
func (b *backend) types(ctx *CmsCtx) {
	// kind 名即控件名（B 方案）: 前端按 kind 直接渲染, 无需 kinds 映射。
	_ = ctx.Json(http.StatusOK, map[string]any{"types": b.eng.Types().Defs()})
}

// ── 节点 CRUD ───────────────────────────────────

// listNodes 按 type 分页列表（管理通道: 含草稿, 全部状态）。
func (b *backend) listNodes(ctx *CmsCtx) {
	typ := ctx.Query("type")
	page := int(ctx.QueryNum("page", 1))
	size := int(ctx.QueryNum("size", 20))
	if size > 100 {
		size = 100
	}
	if typ == "" {
		ctx.Fail(BadRequest("type required"))
		return
	}
	var where gquery.Expr
	filter := strings.TrimSpace(ctx.Query("filter"))
	if filter != "" {
		parsed, err := gquery.ParseLisp(filter, nil)
		if err != nil {
			b.fail(ctx, err)
			return
		}
		where = parsed
	}
	if text := strings.TrimSpace(ctx.Query("q")); text != "" {
		where = gquery.And(where, gquery.Contains(gquery.System("display"), text))
	}
	// 默认 id 降序（新节点在前）; sort 参数可选覆盖。
	sortFields, sortErr := parseSort(ctx.Query("sort"))
	if sortErr != nil {
		b.fail(ctx, sortErr)
		return
	}
	if len(sortFields) == 0 {
		sortFields = []gquery.SortField{gquery.Desc(gquery.System("id"))}
	}
	list, total, err := b.eng.QueryPage(ctx.R.Context(), core.ListQuery{
		Type: typ, Where: where, Scope: core.BypassPolicy(), Sort: sortFields,
		Page: gquery.Page{Number: page, Size: size},
	})
	if err != nil {
		b.fail(ctx, err) // 查询错误 → 422 invalid_query / query_too_complex
		return
	}
	// 列表默认展开全部出边 ref 字段（一层, 批量 — 查询次数=字段数, 与页大小无关）
	expanded, err := b.expandMany(ctx.R.Context(), list)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{
		"items": expanded, "total": total, "page": page, "size": size,
	})
}

// queryNodes 为受管理员认证保护的结构化 QuerySpec 入口。
func (b *backend) queryNodes(ctx *CmsCtx) {
	typeName := ctx.PathValue("type")
	if _, ok := b.eng.Types().Type(typeName); !ok {
		ctx.Fail(NotFound("type not found"))
		return
	}
	where, sortFields, page, err := gquery.DecodeSpec(ctx.R.Body)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	if page.Number <= 0 {
		page.Number = 1
	}
	if page.Size <= 0 {
		page.Size = 20
	}
	page.Size = min(page.Size, 100)
	list, total, err := b.eng.QueryPage(ctx.R.Context(), core.ListQuery{
		Type: typeName, Where: where, Scope: core.BypassPolicy(), Sort: sortFields, Page: page,
	})
	if err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{
		"items": list, "total": total, "page": page.Number, "size": page.Size,
	})
}

// expandMany 列表批量展开全部出边 ref 字段 — "*" 引擎语义（core 解析）。
func (b *backend) expandMany(ctx context.Context, nodes []core.Node) ([]core.Node, error) {
	if len(nodes) == 0 {
		return nodes, nil
	}
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	expanded, err := b.eng.ExpandMany(ctx, ids, b.eng.AutoExpand(nodes[0].Type)...)
	if err != nil {
		return nil, err
	}
	out := make([]core.Node, 0, len(expanded))
	for _, p := range expanded {
		out = append(out, *p)
	}
	return out, nil
}

func (b *backend) createNode(ctx *CmsCtx) {
	typ := ctx.Query("type")
	if typ == "" {
		ctx.Fail(BadRequest("type required"))
		return
	}
	var input struct {
		Display string      `json:"display"`
		Fields  core.Fields `json:"fields"`
	}
	err := ctx.BindStrictJSON(&input)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	node := core.Node{Type: typ, Display: input.Display, Fields: input.Fields}
	if node.Display == "" {
		ctx.Fail(InvalidValue("display required"))
		return
	}
	id, err := b.eng.CreateNode(ctx.R.Context(), &node)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id})
}

func (b *backend) getNode(ctx *CmsCtx) {
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	editable, err := b.eng.FullNode(ctx.R.Context(), id)
	if errors.Is(err, core.ErrNotFound) {
		ctx.Fail(NotFound("not found"))
		return
	}
	if err != nil {
		b.internal(ctx, err)
		return
	}
	node := editable.Node
	node.Fields = editable.Values
	_ = ctx.Json(http.StatusOK, &node)
}

func (b *backend) updateNode(ctx *CmsCtx) {
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	existing, err := b.eng.GetNodeById(ctx.R.Context(), id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if existing == nil {
		ctx.Fail(NotFound("not found"))
		return
	}
	// admin 与 API 统一: 直接用 NodePatch（差量 — nil=不改）。
	// fields 清洗由 core 丢弃未知字段 — admin 无需特殊处理。
	var patch core.NodePatch
	err = ctx.BindStrictJSON(&patch)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	err = b.eng.PatchNode(ctx.R.Context(), id, &patch)
	if errors.Is(err, core.ErrRevisionConflict) {
		ctx.Fail(err)
		return
	}
	if err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) archiveNode(ctx *CmsCtx) {
	b.setNodeArchived(ctx, true)
}

func (b *backend) restoreNode(ctx *CmsCtx) {
	b.setNodeArchived(ctx, false)
}

func (b *backend) setNodeArchived(ctx *CmsCtx, archived bool) {
	id := ctx.PathNum("id", 0)
	if id <= 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	err := ctx.BindStrictJSON(&input)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	if archived {
		err = b.eng.ArchiveNode(ctx.R.Context(), id, input.Revision)
	} else {
		err = b.eng.RestoreNode(ctx.R.Context(), id, input.Revision)
	}
	if errors.Is(err, core.ErrRevisionConflict) {
		ctx.Fail(err)
		return
	}
	if err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) deleteNode(ctx *CmsCtx) {
	id := ctx.PathNum("id", 0)
	if id == 0 {
		ctx.Fail(BadRequest("invalid id"))
		return
	}
	err := b.eng.DeleteNode(ctx.R.Context(), id)
	if errors.Is(err, core.ErrDeleteRestricted) {
		ctx.Fail(err)
		return
	}
	if err != nil {
		ctx.Fail(err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// changePassword 改密（先验旧密）。
func (b *backend) changePassword(ctx *CmsCtx) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := ctx.BindStrictJSON(&in); err != nil {
		b.fail(ctx, err)
		return
	}
	if !b.acct.VerifyPassword("admin", in.OldPassword) {
		ctx.Fail(Unauthorized("old password incorrect"))
		return
	}
	if err := b.acct.SetPassword(in.NewPassword); err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// tree 树视图数据: 某类型全部节点（FullFields 含 parent 引用值）, 不分页。
// 前端按自引用 ref 组装树; 树类型节点少（几十-几百）, 全量可接受。
func (b *backend) tree(ctx *CmsCtx) {
	typ := ctx.Query("type")
	if typ == "" {
		ctx.Fail(BadRequest("type required"))
		return
	}
	list, err := b.eng.Query(ctx.R.Context(), core.ListQuery{
		Type: typ, Scope: core.BypassPolicy(), Page: gquery.Page{Size: 10000},
	})
	if err != nil {
		b.internal(ctx, err)
		return
	}
	items := make([]map[string]any, 0, len(list))
	ids := make([]int64, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	full, err := b.eng.FullNodes(ctx.R.Context(), ids)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	for _, n := range full {
		items = append(items, map[string]any{
			"id": n.ID, "type": n.Type, "display": n.Display,
			"revision": n.Revision, "fields": n.Values,
		})
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": items})
}

// toAny int64 切片 → any 切片（dba expand 参数）。
func toAny(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// inbound 分支子树 in 方向全部节点（无论类型 + 溯源）:
// 参数: node(分支节点), subtree=1(含子树), page, size
// 返回: {items: [{id, type, title, slug, via_field}], total}
func (b *backend) inbound(ctx *CmsCtx) {
	nodeID := ctx.QueryNum("node", 0)
	if nodeID == 0 {
		ctx.Fail(BadRequest("node required"))
		return
	}
	page := int(ctx.QueryNum("page", 1))
	size := int(ctx.QueryNum("size", 20))
	ids := []int64{nodeID}
	if ctx.Query("subtree") == "1" {
		// 分支节点类型 → Subtree（图原语）
		n, err := b.eng.GetNodeById(ctx.R.Context(), nodeID)
		if err != nil || n == nil {
			ctx.Fail(NotFound("node not found"))
			return
		}
		treeCapability, ok := b.eng.Types().Tree(n.Type)
		if !ok {
			ctx.Fail(BadRequest("node type is not a tree"))
			return
		}
		tree, err := b.eng.Subtree(ctx.R.Context(), n.Type, nodeID, treeCapability.Parent, 20)
		if err != nil {
			b.internal(ctx, err)
			return
		}
		ids = append(ids, tree...)
	}
	// in 方向全部节点（无论类型）+ 溯源字段; 分页
	totalPtr, err := b.db.Add(
		`SELECT COUNT(DISTINCT e.from_node) FROM edges e JOIN nodes n ON n.id = e.from_node
		 WHERE e.to_node IN (#{1|expand})`, toAny(ids)).FetchOne[int64]()
	if err != nil {
		b.internal(ctx, err)
		return
	}
	var total int64
	if totalPtr != nil {
		total = *totalPtr
	}
	// inboundRow 溯源行（GROUP BY from_node — 每来源节点取一条边字段）。
	type inboundRow struct {
		ID       int64  `db:"from_node" json:"id"`
		Type     string `db:"type" json:"type"`
		Display  string `db:"display" json:"display"`
		ViaField string `db:"MIN(e.field)" json:"via_field"`
	}
	// 溯源: 每个来源节点取一条边字段（MIN(field) — GROUP BY 去重）
	rows, err := b.db.Add(
		`SELECT e.from_node, n.type, n.display, MIN(e.field) FROM edges e JOIN nodes n ON n.id = e.from_node
		 WHERE e.to_node IN (#{1|expand}) AND n.archived_at IS NULL
		 GROUP BY e.from_node
		 ORDER BY n.updated_at DESC, n.id DESC
		 LIMIT #{2} OFFSET #{3}`,
		toAny(ids), size, (page-1)*size).FetchList[inboundRow]()
	if err != nil {
		b.internal(ctx, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id": r.ID, "type": r.Type, "display": r.Display, "via_field": r.ViaField,
		})
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": items, "total": total})
}

// expand 引用展开预览：文本只作为 typed ExpandPath 的管理端输入前端。
func (b *backend) expand(ctx *CmsCtx) {
	nodeID := ctx.QueryNum("node", 0)
	expr := ctx.Query("expr")
	if nodeID <= 0 {
		ctx.Fail(BadRequest("node required"))
		return
	}
	node, err := b.eng.GetNodeById(ctx.R.Context(), nodeID)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if node == nil {
		ctx.Fail(NotFound("not found"))
		return
	}
	var paths []gquery.ExpandPath
	if expr == "" || expr == "*" {
		paths = b.eng.AutoExpand(node.Type)
	} else {
		paths, err = gquery.ParseExpand(expr)
		if err != nil {
			b.fail(ctx, err)
			return
		}
	}
	root, err := b.eng.Expand(ctx.R.Context(), nodeID, paths...)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"node": root})
}

// ── 站点配置（settings, cmx piece 语义）──────────

// listSettings 全部配置（可按分组过滤）。
func (b *backend) listSettings(ctx *CmsCtx) {
	group := ctx.Query("group")
	list, err := b.eng.ListSettings(ctx.R.Context(), group)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": list})
}

// setSetting body 为 {"key","group","type"(编辑形态),"value"(自由 JSON)}。
func (b *backend) setSetting(ctx *CmsCtx) {
	var in struct {
		Key   string `json:"key"`
		Group string `json:"group"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	if err := ctx.BindStrictJSON(&in); err != nil {
		b.fail(ctx, err)
		return
	}
	if err := b.eng.SetSetting(ctx.R.Context(), in.Key, in.Group, in.Type, in.Value); err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) deleteSetting(ctx *CmsCtx) {
	if err := b.eng.DeleteSetting(ctx.R.Context(), ctx.PathValue("key")); err != nil {
		ctx.Fail(err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// rebuildSearch 全量重建搜索索引（FTS 表被迁移重建/导入后手动触发）。
func (b *backend) mergePreview(ctx *CmsCtx) {
	sourceID := ctx.QueryNum("source", 0)
	targetID := ctx.QueryNum("target", 0)
	if sourceID <= 0 || targetID <= 0 {
		ctx.Fail(BadRequest("source and target required"))
		return
	}
	preview, err := b.eng.PreviewMerge(ctx.R.Context(), sourceID, targetID)
	if err != nil {
		b.fail(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, preview)
}

func (b *backend) relationIntegrity(ctx *CmsCtx) {
	report, err := b.eng.CheckRelations(ctx.R.Context())
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, report)
}

func (b *backend) rebuildSearch(ctx *CmsCtx) {
	if err := b.eng.RebuildSearch(ctx.R.Context()); err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// ── 实体搜索（引用编辑器用）──────────────────────

// search 按 display 模糊搜索节点（type 可选过滤）。
func (b *backend) search(ctx *CmsCtx) {
	q := strings.TrimSpace(ctx.Query("q"))
	typ := ctx.Query("type")
	page := int(ctx.QueryNum("page", 1))
	size := int(ctx.QueryNum("size", 10))
	where := gquery.Expr(nil)
	if q != "" {
		where = gquery.Contains(gquery.System("display"), q)
	}
	if typ != "" {
		list, total, err := b.eng.QueryPage(ctx.R.Context(), core.ListQuery{
			Type: typ, Where: where, Scope: core.BypassPolicy(),
			Page: gquery.Page{Number: page, Size: size},
		})
		if err != nil {
			b.internal(ctx, err)
			return
		}
		_ = ctx.Json(http.StatusOK, map[string]any{"items": list, "total": total})
		return
	}

	items := make([]core.Node, 0)
	var total int64
	for _, typeName := range b.eng.Types().Names() {
		list, count, err := b.eng.QueryPage(ctx.R.Context(), core.ListQuery{
			Type: typeName, Where: where, Scope: core.BypassPolicy(),
			Page: gquery.Page{Number: page, Size: size},
		})
		if err != nil {
			b.internal(ctx, err)
			return
		}
		items = append(items, list...)
		total += count
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > size {
		items = items[:size]
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": items, "total": total})
}
