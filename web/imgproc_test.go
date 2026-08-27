package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// makeTestImage 生成 200x100 红底 PNG。
func makeTestImage(t *testing.T, dir, name string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return p
}

// testSiteWithStatic 带 static 目录的站点。
func testSiteWithStatic(t *testing.T) (*Site, string) {
	t.Helper()
	s := testSite(t)
	dir := t.TempDir()
	makeTestImage(t, dir, "pic.png")
	// 重建 static 挂载（testSite 无 static）— 直接调 mount 需要 spec;
	// 简化: 手动挂 static 路由
	staticDir := dir
	s.Get("/static/*", func(ctx *CmsCtx) {
		serveImg(staticDir, "/static/", ctx, http.StripPrefix("/static", http.FileServer(http.Dir(staticDir))))
	})
	return s, staticDir
}

func TestImgOriginal(t *testing.T) {
	s, _ := testSiteWithStatic(t)
	w := do(s, "GET", "/static/pic.png", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("original = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q", ct)
	}
	if len(w.Body.Bytes()) == 0 {
		t.Fatal("empty body")
	}
}

func TestImgResize(t *testing.T) {
	s, _ := testSiteWithStatic(t)
	w := do(s, "GET", "/static/pic.png?w=100&h=50&mode=cover", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("resize = %d: %s", w.Code, w.Body.String())
	}
	// 解码验证尺寸（RGBA 100x50 → PNG 编码后）
	cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Width != 100 || cfg.Height != 50 {
		t.Fatalf("size = %dx%d", cfg.Width, cfg.Height)
	}
}

func TestImgCacheHit(t *testing.T) {
	s, dir := testSiteWithStatic(t)
	// 第一次生成缓存
	w1 := do(s, "GET", "/static/pic.png?w=80&h=80&mode=crop", nil)
	if w1.Code != http.StatusOK {
		t.Fatalf("first = %d", w1.Code)
	}
	// 缓存文件存在
	cache := filepath.Join(dir, ".cache")
	found := false
	filepath.Walk(cache, func(_ string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("cache file not created")
	}
	// 第二次（命中缓存）
	w2 := do(s, "GET", "/static/pic.png?w=80&h=80&mode=crop", nil)
	if w2.Code != http.StatusOK || w2.Body.Len() != w1.Body.Len() {
		t.Fatalf("cache hit mismatch: %d vs %d", w2.Body.Len(), w1.Body.Len())
	}
}

func TestImgTraversal(t *testing.T) {
	s, _ := testSiteWithStatic(t)
	w := do(s, "GET", "/static/..%2f..%2fetc%2fpasswd?w=10", nil)
	if w.Code == http.StatusOK {
		t.Fatal("traversal should not succeed")
	}
}

func TestImgMissing(t *testing.T) {
	s, _ := testSiteWithStatic(t)
	w := do(s, "GET", "/static/nope.png?w=10", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing = %d", w.Code)
	}
}

func TestImgBadParam(t *testing.T) {
	s, _ := testSiteWithStatic(t)
	w := do(s, "GET", "/static/pic.png?w=abc", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad param = %d", w.Code)
	}
}
