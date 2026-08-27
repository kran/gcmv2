// Package backup 数据库备份插件（后台面板 — AdminMount hook 挂载）。
//
// 能力（第一版）:
//   - 立即备份: SQLite 热备份（VACUUM INTO — 原子一致快照, 无需停服）
//   - 列表 / 下载 / 删除
//   - 恢复不做: 下载后运维手动替换（在线覆盖风险大, 连接池需要重启）
//
// 文件备份（uploads 等）暂不做 — 体积大, 需求未到。
//
// 站点安装（验证 AdminMount hook 能力 — 受保护端点 + 面板菜单）:
//
//	backup.Mount(site, backup.Options{BackupsDir: "backups"})
package backup

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kran/gcmv2/web"
)

// backupItem 备份列表项。
type backupItem struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// Mount 安装备份插件（后台面板）。备份目录读 site.Config()["backups_dir"]
// （缺省 "backups"）。
func Mount(s *web.Site) {
	dir, _ := s.Config()["backups_dir"].(string)
	if dir == "" {
		dir = "backups"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		panic("backup: mkdir: " + err.Error())
	}
	b := &backend{site: s, dir: dir}

	// 受保护端点: Site.Admin() 认证组静态注册（自动登录守卫 — 无 hook 无时序）
	s.Admin().Post("/backup", b.create)
	s.Admin().Get("/backup", b.list)
	s.Admin().Get("/backup/download/{name}", b.download)
	s.Admin().Delete("/backup/{name}", b.delete)
	// 面板组件（插件自己的 Vue SFC — 运行时编译）
	s.Admin().Get("/backup/panel.vue", b.panel)

	// 面板菜单: hook 只返回数据 — /admin/panels 每次请求 Fire（响应式）
	if err := s.Engine().Hooks().AddHook(web.AdminMount,
		func(ctx *web.CmsCtx, panels *[]web.AdminPanel) error {
			*panels = append(*panels, web.AdminPanel{
				Path: "/backup", Title: "备份管理", Vue: "/admin/backup/panel.vue",
			})
			return nil
		}); err != nil {
		panic("backup: panel hook: " + err.Error())
	}
}

// backend 备份 handler 组。
type backend struct {
	site *web.Site
	dir  string
}

// create 立即备份（POST /backup）: VACUUM INTO 一致快照。
func (b *backend) create(ctx *web.CmsCtx) {
	name := "backup-" + time.Now().Format("20060102-150405") + ".db"
	dest := filepath.Join(b.dir, name)
	// VACUUM INTO 路径经 dba 内联参数（引号转义安全 — 路径是我们生成的）
	if _, err := b.site.DB().Add(`VACUUM INTO #{1}`, dest).Exec(); err != nil {
		slog.Error("backup create", "err", err)
		ctx.Error(http.StatusInternalServerError, "backup failed")
		return
	}
	st, err := os.Stat(dest)
	if err != nil {
		slog.Error("backup stat", "err", err)
		ctx.Error(http.StatusInternalServerError, "backup failed")
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{
		"ok": true, "item": backupItem{Name: name, Size: st.Size(), CreatedAt: st.ModTime()},
	})
}

// list 备份列表（GET /backup）— 按时间倒序。
func (b *backend) list(ctx *web.CmsCtx) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "backup: "+err.Error())
		return
	}
	items := make([]backupItem, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "backup-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, backupItem{Name: e.Name(), Size: info.Size(), CreatedAt: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	_ = ctx.Json(http.StatusOK, map[string]any{"items": items})
}

// download 下载备份（GET /backup/download/{name}）— attachment。
func (b *backend) download(ctx *web.CmsCtx) {
	name := safeName(ctx.PathValue("name"))
	if name == "" {
		ctx.Error(http.StatusBadRequest, "bad name")
		return
	}
	full := filepath.Join(b.dir, name)
	if _, err := os.Stat(full); err != nil {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	ctx.SetHeader("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	ctx.SetHeader("Content-Type", "application/octet-stream")
	http.ServeFile(ctx.W, ctx.R, full)
}

// delete 删除备份（DELETE /backup/{name}）。
func (b *backend) delete(ctx *web.CmsCtx) {
	name := safeName(ctx.PathValue("name"))
	if name == "" {
		ctx.Error(http.StatusBadRequest, "bad name")
		return
	}
	if err := os.Remove(filepath.Join(b.dir, name)); err != nil {
		ctx.Error(http.StatusNotFound, "not found")
		return
	}
	_ = ctx.Json(http.StatusOK, map[string]any{"ok": true})
}

// panel 面板组件源码（embed — 无构建运行时编译）。
func (b *backend) panel(ctx *web.CmsCtx) {
	data, err := panelFS.ReadFile("web/backup.vue")
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "panel missing")
		return
	}
	ctx.SetHeader("Content-Type", "text/javascript; charset=utf-8")
	ctx.SetHeader("Cache-Control", "no-cache")
	_, _ = ctx.W.Write(data)
}

// safeName 备份名白名单: backup-*.db（防路径穿越）。
func safeName(name string) string {
	name = filepath.Base(name)
	if strings.HasPrefix(name, "backup-") && strings.HasSuffix(name, ".db") {
		return name
	}
	return ""
}
