package web

import (
	"fmt"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
)

// 图片裁剪: /uploads/xxx.jpg?w=300&h=200&mode=cover（OSS 风格）。
//  - 无 w/h 参数 = 原图直出（现有行为不变）
//  - mode: cover（默认 — 等比缩放后中心裁剪填满）/ fit（等比内嵌不裁）/ crop（精确尺寸中心裁）
//  - 磁盘缓存 uploads/.cache/{w}x{h}-{mode}-{basename} — 处理一次永久直出
//  - w/h 上限 maxImgDim（防恶意超大撑爆内存/CPU）

const maxImgDim = 4000

// imgParams 解析后的裁剪参数。
type imgParams struct {
	W, H int
	Mode string
	Fmt  string // 输出格式: jpg/png（空 = 保持原格式）
}

// parseImgParams 解析查询参数; 三态: (p, true) 处理 / (p, false) 无参数原图 / err 非法参数。
func parseImgParams(r *http.Request) (imgParams, bool, error) {
	q := r.URL.Query()
	w := q.Get("w")
	h := q.Get("h")
	if w == "" && h == "" {
		return imgParams{}, false, nil
	}
	p := imgParams{Mode: "cover"}
	var err error
	if w != "" {
		p.W, err = strconv.Atoi(w)
		if err != nil || p.W < 1 || p.W > maxImgDim {
			return imgParams{}, false, fmt.Errorf("invalid w")
		}
	}
	if h != "" {
		p.H, err = strconv.Atoi(h)
		if err != nil || p.H < 1 || p.H > maxImgDim {
			return imgParams{}, false, fmt.Errorf("invalid h")
		}
	}
	if m := strings.ToLower(q.Get("mode")); m != "" {
		switch m {
		case "cover", "fit", "crop":
			p.Mode = m
		default:
			return imgParams{}, false, fmt.Errorf("invalid mode")
		}
	}
	// fmt: 输出格式（jpg/jpeg/png; 空 = 保持原格式。照片类指定 jpg 体积降 90%）
	if f := strings.ToLower(q.Get("fmt")); f != "" {
		switch f {
		case "jpg", "jpeg":
			p.Fmt = "jpg"
		case "png":
			p.Fmt = "png"
		default:
			return imgParams{}, false, fmt.Errorf("invalid fmt")
		}
	}
	return p, true, nil
}

// imgCachePath 缓存文件路径（basename 保证无路径穿越; 原文件名唯一随机）。
// fmt 指定时缓存扩展名用 fmt（决定 Save 编码格式）。
func imgCachePath(uploads string, p imgParams, reqPath string) string {
	base := filepath.Base(reqPath)
	baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	if p.Fmt != "" {
		ext = "." + p.Fmt // 输出格式由 fmt 决定（照片类 jpg 降体积）
	}
	return filepath.Join(uploads, ".cache", fmt.Sprintf("%dx%d-%s-%s%s", p.W, p.H, p.Mode, baseNoExt, ext))
}

// serveImg 图片处理入口（uploads/static 通用）: 无参数 → 原图直出;
// 有参数 → 缓存命中直出, 否则处理落盘。prefix = "/uploads/" 或 "/static/"。
func serveImg(baseDir, prefix string, ctx *CmsCtx, fs http.Handler) {
	r := ctx.R
	p, ok, err := parseImgParams(r)
	if err != nil {
		ctx.String(http.StatusBadRequest, "img: "+err.Error())
		return
	}
	if !ok {
		fs.ServeHTTP(ctx.W, r) // 无参数 → 原图
		return
	}
	// 缓存命中直出
	cachePath := imgCachePath(baseDir, p, r.URL.Path)
	if st, err := os.Stat(cachePath); err == nil && st.Mode().IsRegular() {
		http.ServeFile(ctx.W, r, cachePath)
		return
	}
	// 原图路径（目录内 — 防穿越）
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(rel, "..") {
		ctx.String(http.StatusNotFound, "404 not found")
		return
	}
	srcPath := filepath.Join(baseDir, filepath.FromSlash(rel))
	// 文件不存在 → 404; 存在但不支持解码（webp 等）→ 原样返回原文件
	if _, err := os.Stat(srcPath); err != nil {
		ctx.String(http.StatusNotFound, "404 not found")
		return
	}
	src, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		fs.ServeHTTP(ctx.W, r) // 不支持格式: 原图直出, 不处理不报错
		return
	}
	dst, err := processImg(src, p)
	if err != nil {
		fs.ServeHTTP(ctx.W, r) // 处理失败: 原图直出
		return
	}
	// 落盘缓存（Save 失败 — jfif 等无编码器的扩展名 — 原图直出不处理）
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		if err := imaging.Save(dst, cachePath); err != nil {
			fs.ServeHTTP(ctx.W, r) // 编码器缺失: 原图直出
			return
		}
	}
	// 输出（直接写缓存文件 — 与落盘一致）
	http.ServeFile(ctx.W, r, cachePath)
}

// processImg 按参数处理: cover = Fill（缩放+中心裁填满）; fit = Fit（等比内嵌）;
// crop = 精确尺寸中心裁。单边省略 = 按比例缩放另一边。
func processImg(src image.Image, p imgParams) (image.Image, error) {
	var dst image.Image
	switch p.Mode {
	case "fit":
		if p.W == 0 {
			p.W = int(float64(src.Bounds().Dx()) * float64(p.H) / float64(src.Bounds().Dy()))
		}
		if p.H == 0 {
			p.H = int(float64(src.Bounds().Dy()) * float64(p.W) / float64(src.Bounds().Dx()))
		}
		dst = imaging.Fit(src, p.W, p.H, imaging.Lanczos)
	case "crop":
		if p.W == 0 || p.H == 0 {
			return nil, fmt.Errorf("crop requires both w and h")
		}
		dst = imaging.CropCenter(src, p.W, p.H)
	default: // cover
		if p.W == 0 {
			p.W = int(float64(src.Bounds().Dx()) * float64(p.H) / float64(src.Bounds().Dy()))
		}
		if p.H == 0 {
			p.H = int(float64(src.Bounds().Dy()) * float64(p.W) / float64(src.Bounds().Dx()))
		}
		dst = imaging.Fill(src, p.W, p.H, imaging.Center, imaging.Lanczos)
	}
	return dst, nil
}
