package backup

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kran/gcmv2/web"
)

// testSite 建站点 + 装 backup 插件 + admin 登录。
func testSite(t *testing.T) (*web.Site, string) {
	t.Helper()
	dir := t.TempDir()
	tp := filepath.Join(dir, "types.yaml")
	os.WriteFile(tp, []byte(`
types:
  article:
    fields:
      - { name: body, kind: richtext }
`), 0o644)
	tdir := filepath.Join(dir, "templates")
	os.MkdirAll(tdir, 0o755)
	site, err := web.NewSite(web.SiteSpec{
		DBPath: filepath.Join(dir, "test.db"), Types: tp,
		Templates: tdir, Migrate: true, AdminPass: "cmx12345",
		Config: map[string]any{"backups_dir": filepath.Join(dir, "backups")},
	})
	if err != nil {
		t.Fatal(err)
	}
	Mount(site)
	return site, filepath.Join(dir, "backups")
}

// login 登录拿 cookie。
func login(t *testing.T, s *web.Site) *http.Cookie {
	t.Helper()
	w := do(s, "POST", "/admin/login", map[string]any{"username": "admin", "password": "cmx12345"})
	if w.Code != http.StatusOK {
		t.Fatalf("login = %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "gcm_admin" {
			return c
		}
	}
	t.Fatal("no cookie")
	return nil
}

func TestBackupCreateListDownloadDelete(t *testing.T) {
	s, dir := testSite(t)
	ck := login(t, s)

	// 立即备份
	w := do(s, "POST", "/admin/backup", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Item struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Item.Name == "" || out.Item.Size == 0 {
		t.Fatalf("bad item: %+v", out.Item)
	}
	// 备份文件是合法 SQLite（能打开）
	db, err := sql.Open("sqlite3", filepath.Join(dir, out.Item.Name))
	if err == nil {
		db.Close()
	} else {
		// 用 modernc 驱动名（dba 的）— 直接检查文件头（SQLite magic）
		f, _ := os.Open(filepath.Join(dir, out.Item.Name))
		hdr := make([]byte, 16)
		f.Read(hdr)
		f.Close()
		if string(hdr[:6]) != "SQLite" {
			t.Fatal("backup file not sqlite")
		}
	}

	// 列表
	w = do(s, "GET", "/admin/backup", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d", w.Code)
	}
	var list struct {
		Items []backupItem `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].Name != out.Item.Name {
		t.Fatalf("items = %+v", list.Items)
	}

	// 下载
	w = do(s, "GET", "/admin/backup/download/"+out.Item.Name, nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("download = %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("no attachment header")
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty download")
	}

	// 删除
	w = do(s, "DELETE", "/admin/backup/"+out.Item.Name, nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("delete = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, out.Item.Name)); err == nil {
		t.Fatal("file should be deleted")
	}
}

func TestBackupAuthRequired(t *testing.T) {
	s, _ := testSite(t)
	// 路由静态注册（Admin 组守卫 — 注册即绑定）— 未登录全部端点 401
	cases := []struct {
		method, path string
	}{
		{"POST", "/admin/backup"},
		{"GET", "/admin/backup"},
		{"GET", "/admin/backup/download/x.db"},
		{"DELETE", "/admin/backup/x.db"},
		{"GET", "/admin/backup/panel.vue"}, // 面板组件同样受保护
	}
	for _, c := range cases {
		w := do(s, c.method, c.path, nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s = %d, want 401", c.method, c.path, w.Code)
		}
	}
}

func TestBackupTraversal(t *testing.T) {
	s, _ := testSite(t)
	ck := login(t, s)
	// 路径穿越 → 400
	w := do(s, "GET", "/admin/backup/download/..%2f..%2fetc%2fpasswd", nil, ck)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("traversal = %d", w.Code)
	}
}

func TestBackupPanel(t *testing.T) {
	s, _ := testSite(t)
	ck := login(t, s)
	w := do(s, "GET", "/admin/backup/panel.vue", nil, ck)
	if w.Code != http.StatusOK {
		t.Fatalf("panel = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "备份管理") {
		t.Fatal("panel content missing")
	}
}

// ── 小工具 ──

func do(s *web.Site, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}
