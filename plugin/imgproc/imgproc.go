// Package imgproc 图片裁剪插件 — 经 HookServeFile 生效（可选 — 不装 = 文件直出）。
//
// /static /uploads 端点 Fire HookServeFile; 本插件 AddHook:
//   - 无裁剪参数 → 不动 filePath（原图直出）
//   - 有裁剪参数 → 处理落盘缓存 → 改 *filePath 指向缓存（mount 兜底 ServeFile 用）
package imgproc

import (
	"fmt"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
	"github.com/kran/gcmv2/web"
)

const maxImgDim = 4000

// imgParams 裁剪参数。
type imgParams struct {
	W, H int
	Mode string
	Fmt  string
}

// Mount 安装 imgproc 插件（AddHook HookServeFile — 图片裁剪）。
func Mount(s *web.Site) {
	s.Hook(web.HookServeFile, ServeFile)
}

// ServeFile HookServeFile handler: 无裁参不动 filePath; 有则处理 → 改 filePath。
func ServeFile(ctx *web.CmsCtx, filePath *string) error {
	p, ok, err := parseImgParams(ctx.R)
	if err != nil {
		return fmt.Errorf("img: %w", err) // 不写响应 — serveFiles 统一错误出口（防双写）
	}
	if !ok {
		return nil // 无参数 — 原图直出（不改 filePath）
	}
	cachePath := imgCachePath(*filePath, p, ctx.R.URL.Path)
	if st, err := os.Stat(cachePath); err == nil && st.Mode().IsRegular() {
		*filePath = cachePath // 缓存命中 — 指向缓存
		return nil
	}
	srcPath := *filePath
	if _, err := os.Stat(srcPath); err != nil {
		return nil // 原图不存在 — 兜底 404
	}
	src, err := imaging.Open(srcPath, imaging.AutoOrientation(true))
	if err != nil {
		return nil // 不支持格式 — 原图直出
	}
	dst, err := processImg(src, p)
	if err != nil {
		return nil // 处理失败 — 原图直出
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil
	}
	if err := imaging.Save(dst, cachePath); err != nil {
		return nil // 编码器缺失 — 原图直出
	}
	*filePath = cachePath // 处理落盘 → 改 filePath 指向缓存
	return nil
}

// parseImgParams 解析查询参数; 三态: (p, true) 处理 / (p, false) 无参数原图 / err 非法参数。
func parseImgParams(r *http.Request) (imgParams, bool, error) {
	q := r.URL.Query()
	if proc := q.Get("x-oss-process"); proc != "" {
		return parseOSSProcess(proc)
	}
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

// imgCachePath 缓存路径（原文件 filePath 同目录 .cache）。
func imgCachePath(filePath string, p imgParams, reqPath string) string {
	base := filepath.Base(reqPath)
	baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
	ext := filepath.Ext(base)
	if p.Fmt != "" {
		ext = "." + p.Fmt
	}
	return filepath.Join(filepath.Dir(filePath), ".cache", fmt.Sprintf("%dx%d-%s-%s%s", p.W, p.H, p.Mode, baseNoExt, ext))
}

// parseOSSProcess 解析 OSS 图片处理参数（x-oss-process 子集）。
func parseOSSProcess(proc string) (imgParams, bool, error) {
	p := imgParams{Mode: "cover"}
	for _, seg := range strings.Split(proc, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		switch {
		case seg == "image/resize":
		case strings.HasPrefix(seg, "image/"):
			return imgParams{}, false, fmt.Errorf("img: unsupported process %q", seg)
		case strings.HasPrefix(seg, "w_"):
			n, err := strconv.Atoi(strings.TrimPrefix(seg, "w_"))
			if err != nil || n < 1 || n > maxImgDim {
				return imgParams{}, false, fmt.Errorf("img: invalid w_")
			}
			p.W = n
		case strings.HasPrefix(seg, "h_"):
			n, err := strconv.Atoi(strings.TrimPrefix(seg, "h_"))
			if err != nil || n < 1 || n > maxImgDim {
				return imgParams{}, false, fmt.Errorf("img: invalid h_")
			}
			p.H = n
		case seg == "m_fill":
			p.Mode = "cover"
		case seg == "m_lfit":
			p.Mode = "fit"
		default:
			return imgParams{}, false, fmt.Errorf("img: unsupported process segment %q", seg)
		}
	}
	if p.W == 0 && p.H == 0 {
		return imgParams{}, false, fmt.Errorf("img: resize needs w_ or h_")
	}
	return p, true, nil
}

// processImg 按参数处理（cover=Fill / fit=Fit / crop=CropCenter）。
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
	default:
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
