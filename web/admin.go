// admin 管理后端（web 包内 — 登录 + 节点管理 API + 引用编辑）。: 登录（accounts + bcrypt + session cookie）+
// 按 type 分组的实体管理 API + 引用编辑（ref 字段经 fields 提交, 引擎落边）。
// 单账号每库（accounts 表, 每站一库无 site 列）。
package web

import (
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
	"strconv"
	"strings"
	"time"

	"github.com/kran/cho"
	"github.com/kran/dba"
	"github.com/kran/gcmv2/core"
	"github.com/kran/gcmv2/types"
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
	ID           int64     `db:"id,omitempty"`
	Username     string    `db:"username"`
	PasswordHash string    `db:"password_hash"`
	SessionKey   string    `db:"session_key"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
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
	res, err := s.db.Update("accounts",
		dba.H{"password_hash": string(hash), "updated_at": time.Now()}, "1 = 1").Exec()
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
	res, err := s.db.Update("accounts",
		dba.H{"session_key": key, "updated_at": time.Now()}, "1 = 1").Exec()
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
	return err == nil && a != nil && a.SessionKey == key
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
	acct      *AdminService
	eng       core.Engine
	db        *dba.SQL
	uploadDir string
	panels    []AdminPanel // 站点面板（AdminMount 收集）
}

// internal 服务器错误统一出口: 细节进日志, 响应透传（admin 是站长工具,
// fail-loud 可见性优先; 敏感 SQL 细节留在日志）。
func (b *backend) internal(ctx *CmsCtx, err error) {
	slog.Error("admin internal", "path", ctx.R.URL.Path, "err", err)
	_ = ctx.Error(http.StatusInternalServerError, "internal error")
}

// bad 客户端错误（校验/参数）: 直接透传, 前端表单回显。
func (b *backend) bad(ctx *CmsCtx, err error) {
	ctx.Error(http.StatusBadRequest, err.Error())
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

// uploadAllowExt 上传扩展名白名单。svg/html 刻意排除（可执行内容,
// 同源服务 = 存储型 XSS）。
var uploadAllowExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".ico": true,
	".pdf": true, ".zip": true,
	".mp4": true, ".webm": true, ".mp3": true, ".wav": true,
}

// upload 处理 multipart 上传: 大小上限 → 扩展名白名单 → 随机文件名 → 落 uploads。
func (b *backend) upload(ctx *CmsCtx) {
	if b.uploadDir == "" {
		ctx.Error(http.StatusBadRequest, "uploads disabled")
		return
	}
	maxBytes := int64(8 << 20) // 8MB 硬上限
	ctx.R.Body = http.MaxBytesReader(ctx.W, ctx.R.Body, maxBytes)
	if err := ctx.R.ParseMultipartForm(maxBytes); err != nil {
		ctx.Error(http.StatusRequestEntityTooLarge, "file too large (max 8MB)")
		return
	}
	f, fh, err := ctx.R.FormFile("file")
	if err != nil {
		ctx.Error(http.StatusBadRequest, "missing file field 'file'")
		return
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	if !uploadAllowExt[ext] {
		ctx.Error(http.StatusBadRequest, "file type not allowed: "+ext)
		return
	}
	base := strings.TrimSuffix(filepath.Base(fh.Filename), filepath.Ext(fh.Filename))
	base = strings.ReplaceAll(base, " ", "-")
	rand4 := make([]byte, 4)
	if _, err := rand.Read(rand4); err != nil {
		b.internal(ctx, err)
		return
	}
	name := fmt.Sprintf("%d-%s-%s%s", time.Now().Unix(), hex.EncodeToString(rand4), base, ext)
	if err := os.MkdirAll(b.uploadDir, 0755); err != nil {
		b.internal(ctx, err)
		return
	}
	dst, err := os.Create(filepath.Join(b.uploadDir, name))
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if _, err := io.Copy(dst, f); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		b.internal(ctx, err)
		return
	}
	dst.Close()
	_ = ctx.Json(http.StatusOK, map[string]any{"name": name, "path": "/uploads/" + name})
}

// AdminPanel 一个后台面板（站点专门管理: 菜单项 + 组件入口）。
type AdminPanel struct {
	Path  string `json:"path"`  // 组内 API 前缀, 如 "/guestbook"
	Title string `json:"title"` // 后台菜单名
	Vue   string `json:"vue"`   // 面板组件完整 URL（认证路由, 站点自己挂）
}

// AdminMount 后台面板挂载事件（admin.Mount 建 /admin 认证组后 Fire）:
// 原型 func(g *cho.Cho[*CmsCtx], panels *[]AdminPanel) error —
// 站点 AddHook 挂自己的受保护端点（自动带登录守卫）+ 注册后台菜单。
const AdminMount = "admin.mount"

// Mount 挂载 /admin 组到站点（登录保护; 公开入口: login/ui/upload）。
// uploadDir 是上传落盘目录（可为空 = 禁用上传）; /uploads/* 服务由站点
// 装配层挂载（gcm.NewApp — 前台资源不依赖 admin 是否启用）。
// svc/ts 从 site 上下文取（site.Service() / svc.Types()）— 装配参数最小化。
// DefineHooks 声明后台事件（AdminMount — 装配早期调用, 站点 Setup/AddHook 前）;
// 与 defineWebHooks 同位置（NewSite 装配序列）。
func defineAdminHooks(svc *core.Service) error {
	return svc.Hooks().Define(core.HookSpec{Name: AdminMount,
		Proto: func(*cho.Cho[*CmsCtx], *[]AdminPanel) error { return nil }})
}

func (s *Site) mountAdmin(uploadDir string) {
	b := &backend{acct: NewService(s.DB()), eng: s.eng, db: s.DB(), uploadDir: uploadDir}

	// /admin 组: 公开 login/logout/ui/upload, 其余登录保护
	s.Group("/admin", func(g *cho.Cho[*CmsCtx]) {
		g.Post("/login", b.login)
		g.Post("/logout", b.logout)
		g.Get("/ui/*", b.uiFile)
		g.Post("/upload", b.upload)
		g.Group("", func(authed *cho.Cho[*CmsCtx]) {
			authed.UseCtx(b.requireAuth)
			// 站点专门管理: Fire AdminMount — 站点挂受保护端点 + 注册面板
			panels := &[]AdminPanel{}
			if err := b.eng.Hooks().Fire(AdminMount, authed, panels); err != nil {
				panic(fmt.Sprintf("admin: fire mount hook: %v", err))
			}
			b.panels = *panels
			authed.Get("/panels", b.listPanels)
			authed.Get("/me", b.me)
			authed.Get("/types", b.types)
			authed.Get("/nodes", b.listNodes)
			authed.Post("/nodes", b.createNode)
			authed.Get("/nodes/{id}", b.getNode)
			authed.Put("/nodes/{id}", b.updateNode)
			authed.Delete("/nodes/{id}", b.deleteNode)
			authed.Get("/search", b.search)
			authed.Get("/tree", b.tree)
			authed.Get("/inbound", b.inbound)
			authed.Get("/expand", b.expand)
			authed.Post("/password", b.changePassword)
			authed.Get("/settings", b.listSettings)
			authed.Post("/settings", b.setSetting)
			authed.Delete("/settings/{key}", b.deleteSetting)
		})
	})
}

// ── 认证 ─────────────────────────────────────────

func (b *backend) login(ctx *CmsCtx) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	if !b.acct.VerifyPassword(in.Username, in.Password) {
		ctx.Error(http.StatusUnauthorized, "invalid credentials")
		return
	}
	key, err := b.acct.NewSession()
	if err != nil {
		b.internal(ctx, err)
		return
	}
	http.SetCookie(ctx.W, &http.Cookie{
		Name: cookieName, Value: key,
		Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(sessionTTL),
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) logout(ctx *CmsCtx) {
	http.SetCookie(ctx.W, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
	})
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// listPanels 站点面板列表（前端动态注册菜单 + 组件）。
func (b *backend) listPanels(ctx *CmsCtx) {
	_ = ctx.Json(http.StatusOK, map[string]any{"items": b.panels})
}

// requireAuth 会话校验中间件（cho 类型化中间件: 校验失败短路）。
func (b *backend) requireAuth(ctx *CmsCtx, next func()) {
	c, err := ctx.R.Cookie(cookieName)
	if err != nil || !b.acct.ValidSession(c.Value) {
		ctx.Error(http.StatusUnauthorized, "unauthorized")
		return
	}
	next()
}

func (b *backend) me(ctx *CmsCtx) {
	_ = ctx.Json(http.StatusOK, map[string]any{"username": "admin"})
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
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 20)
	if size > 100 {
		size = 100
	}
	if typ == "" {
		ctx.Error(http.StatusBadRequest, "type required")
		return
	}
	// filter 参数: 完整查询能力（树过滤/任意字段筛选）; q 参数: 标题模糊搜索
	// （like 参数化, 与 filter 叠加）。表达式经 filter 引擎编译 + 参数化。
	var list []core.Node
	var total int64
	var err error
	filter := strings.TrimSpace(ctx.Query("filter"))
	q := strings.TrimSpace(ctx.Query("q"))
	// filter = Lisp 表达式; q = 标题模糊; status = 状态过滤（1 发布/0 草稿）
	params := map[string]any{}
	if q != "" {
		params["q"] = "%" + q + "%"
		if filter != "" {
			filter = "(and " + filter + " (like title {:q}))"
		} else {
			filter = "(like title {:q})"
		}
	}
	if st := ctx.Query("status"); st != "" {
		n, err := strconv.Atoi(st)
		if err == nil {
			params["st"] = n
			filter = "(and (= status {:st}) " + filter + ")" // status 过滤
		}
	}
	// 统一 Q: 类型过滤合成（参数化 (= type {:typ})）
	params["typ"] = typ
	f := `(= type {:typ})`
	if filter != "" {
		f = `(and (= type {:typ}) ` + filter + `)`
	}
	list, total, err = b.eng.QueryPage(core.ListQuery{Filter: f, Page: page, Size: size}, params)
	if err != nil {
		// filter 编译错误（filter-lisp: 前缀）= 客户端参数 → 400; 其余 → 500
		if strings.Contains(err.Error(), "filter-lisp:") {
			b.bad(ctx, err)
		} else {
			b.internal(ctx, err)
		}
		return
	}
	// 列表默认展开全部出边 ref 字段（一层, 批量 — 查询次数=字段数, 与页大小无关）
	expanded, err := b.expandMany(list)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{
		"items": expanded, "total": total, "page": page, "size": size,
	})
}

// expandMany 列表批量展开全部出边 ref 字段 — "*" 引擎语义（core 解析）。
func (b *backend) expandMany(nodes []core.Node) ([]core.Node, error) {
	if len(nodes) == 0 {
		return nodes, nil
	}
	ids := make([]int64, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	expanded, err := b.eng.ExpandPathMany(ids, "*")
	if err != nil {
		return nil, err
	}
	out := make([]core.Node, 0, len(expanded))
	for _, p := range expanded {
		out = append(out, *p)
	}
	return out, nil
}

// nodeInput 创建/更新输入: 列字段 + 类型字段（含 ref 字段 — 引擎落边）。
// Status/Sort 指针: nil = 未提交（保持现值）, 非 nil = 提交（0 合法 — 草稿）。
type nodeInput struct {
	Slug   string      `json:"slug"`
	Status *int        `json:"status"`
	Sort   *int        `json:"sort"`
	Fields core.Fields `json:"fields"`
}

func (b *backend) createNode(ctx *CmsCtx) {
	typ := ctx.Query("type")
	if typ == "" {
		ctx.Error(http.StatusBadRequest, "type required")
		return
	}
	var in nodeInput
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	status, sort := 0, 0
	if in.Status != nil {
		status = *in.Status
	}
	if in.Sort != nil {
		sort = *in.Sort
	}
	id, err := b.eng.CreateNode(&core.Node{
		Type: typ, Slug: in.Slug, Status: status, Sort: sort, Fields: in.Fields,
	})
	if err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusCreated, map[string]any{"id": id})
}

func (b *backend) getNode(ctx *CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	n, err := b.eng.GetNodeById(id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if n == nil {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	// 管理视图: fields + ref 字段值（编辑回显）
	fields, err := b.eng.FullFields(id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	n.Fields = fields
	_ = ctx.Json(http.StatusOK, n)
}

func (b *backend) updateNode(ctx *CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	existing, err := b.eng.GetNodeById(id)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	if existing == nil {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	var in nodeInput
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	// 全量语义: PatchFromNode 快照现值 → 按提交覆盖（未传列保持现值）
	patch := core.PatchFromNode(existing)
	if in.Slug != "" {
		patch.Slug = &in.Slug
	}
	if in.Status != nil {
		patch.Status = in.Status
	}
	if in.Sort != nil {
		patch.Sort = in.Sort
	}
	// 全量语义下 fields 可能带回历史脏数据（类型收敛前的遗留字段）—
	// 提交前清洗: 只保留类型声明的字段（保存即自愈）
	td, _ := b.eng.Types().Type(existing.Type)
	clean := map[string]any{}
	for name, v := range in.Fields {
		if _, ok := types.FieldByName(td, name); ok {
			clean[name] = v
		}
	}
	patch.Fields = clean
	if err := b.eng.PatchNode(id, patch); err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) deleteNode(ctx *CmsCtx) {
	id, err := strconv.ParseInt(ctx.PathValue("id"), 10, 64)
	if err != nil {
		ctx.Error(http.StatusBadRequest, "invalid id")
		return
	}
	if err := b.eng.DeleteNode(id); err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
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
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	if !b.acct.VerifyPassword("admin", in.OldPassword) {
		ctx.Error(http.StatusUnauthorized, "old password incorrect")
		return
	}
	if err := b.acct.SetPassword(in.NewPassword); err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// tree 树视图数据: 某类型全部节点（FullFields 含 parent 引用值）, 不分页。
// 前端按自引用 ref 组装树; 树类型节点少（几十-几百）, 全量可接受。
func (b *backend) tree(ctx *CmsCtx) {
	typ := ctx.Query("type")
	if typ == "" {
		ctx.Error(http.StatusBadRequest, "type required")
		return
	}
	list, err := b.eng.Query(core.ListQuery{Filter: `(= type {:t})`, Size: 10000},
		map[string]any{"t": typ})
	if err != nil {
		b.internal(ctx, err)
		return
	}
	items := make([]map[string]any, 0, len(list))
	for _, n := range list {
		fields, err := b.eng.FullFields(n.ID)
		if err != nil {
			b.internal(ctx, err)
			return
		}
		items = append(items, map[string]any{
			"id": n.ID, "type": n.Type, "title": n.Title, "slug": n.Slug,
			"status": n.Status, "sort": n.Sort, "fields": fields,
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
	nodeID := int64(ctx.QueryInt("node", 0))
	if nodeID == 0 {
		ctx.Error(http.StatusBadRequest, "node required")
		return
	}
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 20)
	ids := []int64{nodeID}
	if ctx.Query("subtree") == "1" {
		// 分支节点类型 → Subtree（图原语）
		n, err := b.eng.GetNodeById(nodeID)
		if err != nil || n == nil {
			ctx.Error(http.StatusBadRequest, "node not found")
			return
		}
		tree, err := b.eng.Subtree(n.Type, nodeID, "parent", 20)
		if err == nil {
			ids = append(ids, tree...)
		}
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
		Title    string `db:"title" json:"title"`
		Slug     string `db:"slug" json:"slug"`
		ViaField string `db:"MIN(e.field)" json:"via_field"`
	}
	// 溯源: 每个来源节点取一条边字段（MIN(field) — GROUP BY 去重）
	rows, err := b.db.Add(
		`SELECT e.from_node, n.type, n.title, n.slug, MIN(e.field) FROM edges e JOIN nodes n ON n.id = e.from_node
		 WHERE e.to_node IN (#{1|expand})
		 GROUP BY e.from_node
		 ORDER BY n.sort, n.id DESC
		 LIMIT #{2} OFFSET #{3}`,
		toAny(ids), size, (page-1)*size).FetchList[inboundRow]()
	if err != nil {
		b.internal(ctx, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id": r.ID, "type": r.Type, "title": r.Title, "slug": r.Slug, "via_field": r.ViaField,
		})
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": items, "total": total})
}

// expand 引用展开预览（ExpandPath）: expr 为空 = 该类型全部 ref 字段一层全景。
func (b *backend) expand(ctx *CmsCtx) {
	nodeID := int64(ctx.QueryInt("node", 0))
	expr := ctx.Query("expr")
	if nodeID <= 0 {
		ctx.Error(http.StatusBadRequest, "node required")
		return
	}
	if expr == "" || expr == "*" {
		// 自动 / "*": 该类型所有 ref 字段（出边, 逗号并行, 一层）
		n, err := b.eng.GetNodeById(nodeID)
		if err != nil {
			b.internal(ctx, err)
			return
		}
		if n == nil {
			ctx.Error(http.StatusNotFound, "not found")
			return
		}
		expr = "*" // 引擎语义: 该类型全部出边 ref 字段
	}
	root, err := b.eng.ExpandPath(nodeID, expr)
	if err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"node": root})
}

// ── 站点配置（settings, cmx piece 语义）──────────

// listSettings 全部配置（可按分组过滤）。
func (b *backend) listSettings(ctx *CmsCtx) {
	group := ctx.Query("group")
	list, err := b.eng.ListSettings(group)
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
	if err := ctx.BindJson(&in); err != nil {
		b.bad(ctx, err)
		return
	}
	if err := b.eng.SetSetting(in.Key, in.Group, in.Type, in.Value); err != nil {
		b.bad(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

func (b *backend) deleteSetting(ctx *CmsCtx) {
	if err := b.eng.DeleteSetting(ctx.PathValue("key")); err != nil {
		ctx.Error(http.StatusNotFound, err.Error())
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// ── 实体搜索（引用编辑器用）──────────────────────

// search 按标题/slug 模糊搜索节点（type 可选过滤）。
func (b *backend) search(ctx *CmsCtx) {
	q := strings.TrimSpace(ctx.Query("q"))
	typ := ctx.Query("type")
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 10)
	// Lisp 合成: type 可选过滤 + title/slug 模糊
	f := ""
	params := map[string]any{}
	if typ != "" {
		f = `(= type {:typ})`
		params["typ"] = typ
	}
	if q != "" {
		like := `(or (like title {:q}) (like slug {:q}))`
		if f != "" {
			f = `(and ` + f + ` ` + like + `)`
		} else {
			f = like
		}
		params["q"] = "%" + q + "%"
	}
	list, total, err := b.eng.QueryPage(core.ListQuery{Filter: f, Page: page, Size: size}, params)
	if err != nil {
		b.internal(ctx, err)
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"items": list, "total": total})
}
