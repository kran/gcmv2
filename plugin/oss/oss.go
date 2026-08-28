// Package oss 阿里云 OSS 链接模板函数插件（配合 OSS 镜像回源）。
//
// 原理: OSS bucket 配置"镜像回源"指向站点（源站）— 桶里没有的对象自动
// 回源拉取并缓存。模板里把 /uploads/ 文件直接指向桶地址 — 流量走 OSS
// CDN 加速, 服务器只兜底回源。
//
// 安装:
//
//	oss.Mount(site)   // 桶地址读 site.Config()["oss_bucket"]
//	  - 配置 → 桶链接（OSS 处理 + 镜像回源）
//	  - 空/未配 → 本地模式（同一 x-oss-process 参数走本地 imgproc）
//
// 模板用法（管道友好 — 主体在最后）:
//
//	{{ $n.Fields.cover | oss }}                    // 原图直链
//	{{ $n.Fields.cover | oss 600 450 }}            // 裁切（默认 cover → m_fill）
//	{{ $n.Fields.cover | oss 600 450 "fit" }}      // 等比缩放 → m_lfit
//	{{ $n.Fields.video | oss }}                    // 视频直链（无处理参数）
//
// 非 /uploads/ 链接（外部/CDN）原样返回 — 不误转。
// 裁切用 OSS 原生图片处理（x-oss-process）— 不占本站服务器。
package oss

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"

	"github.com/kran/gcmv2/web"
)

// Mount 安装 oss 模板函数。
// 管道/非管道都支持（管道把主体放最后 — 自动识别）:
//
//	{{ $n.Fields.cover | oss 600 450 }}   → args = [600 450 url] — url 在最后
//	{{ oss $n.Fields.cover 600 450 }}     → args = [url 600 450] — url 在首位
func Mount(s *web.Site) {
	bucket, _ := s.Config()["oss_bucket"].(string)
	bucket = strings.TrimSuffix(bucket, "/")
	s.Func("oss", func(args ...any) string {
		if len(args) == 0 {
			return ""
		}
		var url string
		var rest []any
		if u, ok := args[0].(string); ok && strings.HasPrefix(u, "/uploads/") {
			url, rest = u, args[1:] // 非管道
		} else {
			url, rest = fmt.Sprint(args[len(args)-1]), args[:len(args)-1] // 管道
		}
		return OSSURL(bucket, url, rest...)
	})
	s.Func("rich", func(html any) template.HTML {
		return Rich(bucket, html)
	})
}

// OSSURL 核心（导出 — 单测）: /uploads/ 链接 → 桶链接（+ OSS 图片处理参数）。
// bucket 为空 = 本地模式: 返回原路径 + x-oss-process 参数（本地 imgproc 识别
// 同一格式 — 线上 OSS 处理 / 线下本地处理, 一套参数两边用）。
// 非 /uploads/ 原样返回。裁切参数: [w [h [mode]]] — mode: cover(默认)/fit/crop。
func OSSURL(bucket, url string, args ...any) string {
	if !strings.HasPrefix(url, "/uploads/") {
		return url // 非本站上传 — 原样
	}
	out := bucket + url
	// 视频/音频不拼图片处理参数（OSS 视频处理是另一套 video/ 参数）
	if isMedia(url) {
		return out
	}
	w, h := 0, 0
	mode := ""
	if len(args) > 0 {
		w = toInt(args[0])
	}
	if len(args) > 1 {
		h = toInt(args[1])
	}
	if len(args) > 2 {
		mode = fmt.Sprint(args[2])
	}
	if w > 0 || h > 0 {
		// OSS resize: 逗号连接（m_fill = 裁剪填充 ≈ cover; m_lfit = 等比 ≈ fit;
		// crop 无直接对应 — 回落 fill）
		m := "m_fill"
		if mode == "fit" {
			m = "m_lfit"
		}
		proc := "?x-oss-process=image/resize"
		if w > 0 {
			proc += fmt.Sprintf(",w_%d", w)
		}
		if h > 0 {
			proc += fmt.Sprintf(",h_%d", h)
		}
		proc += "," + m
		out += proc
	}
	return out
}

// Rich 处理富文本内嵌媒体 — 替换 <img|video|audio src="/uploads/..."> 的 src
// 为 oss URL（桶链接 / 本地原路径 — 复用 OSSURL）。外部 URL/CDN 不转。
// html 为 any（字段缺失/为 null 时安全返回空 — 不报错）。
// 返回 template.HTML（富文本信任输出 — 模板直接 {{ body | rich }}）。
func Rich(bucket string, html any) template.HTML {
	s, ok := html.(string)
	if !ok || s == "" {
		return ""
	}
	re := regexp.MustCompile(`src="(/uploads/[^"]*)"`)
	out := re.ReplaceAllStringFunc(s, func(m string) string {
		url := re.FindStringSubmatch(m)[1]
		return `src="` + OSSURL(bucket, url) + `"`
	})
	return template.HTML(out)
}

// isMedia 视频/音频扩展名（图片处理参数不适用）。
func isMedia(url string) bool {
	ext := strings.ToLower(url)
	for _, e := range []string{".mp4", ".webm", ".mp3", ".wav", ".ogg"} {
		if strings.HasSuffix(ext, e) {
			return true
		}
	}
	return false
}

// toInt 任意数值 → int（模板传参 JSON number / int）。
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	}
	return 0
}
