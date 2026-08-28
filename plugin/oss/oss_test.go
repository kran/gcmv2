package oss

import (
	"strings"
	"testing"
)

func TestOSSURL(t *testing.T) {
	bucket := "https://viicn-files.oss-cn-beijing.aliyuncs.com"
	cases := []struct {
		url  string
		args []any
		want string
	}{
		{"/uploads/a.jpg", nil,
			"https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/a.jpg"},
		{"/uploads/a.jpg", []any{600, 450},
			"https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/a.jpg?x-oss-process=image/resize,w_600,h_450,m_fill"},
		{"/uploads/a.jpg", []any{600, 450, "fit"},
			"https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/a.jpg?x-oss-process=image/resize,w_600,h_450,m_lfit"},
		{"/uploads/a.jpg", []any{0, 300},
			"https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/a.jpg?x-oss-process=image/resize,h_300,m_fill"},
		{"/uploads/a.jpg", []any{float64(600)}, // 模板传参 JSON number
			"https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/a.jpg?x-oss-process=image/resize,w_600,m_fill"},
		{"/uploads/v.mp4", []any{600}, // 视频不拼处理参数（OSS 视频处理另一套）
			"https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/v.mp4"},
		{"https://cdn.example.com/x.jpg", []any{600},
			"https://cdn.example.com/x.jpg"}, // 外部链接原样
		{"/static/x.jpg", []any{600},
			"/static/x.jpg"}, // 非 uploads 原样
	}
	for _, c := range cases {
		got := OSSURL(bucket, c.url, c.args...)
		if got != c.want {
			t.Fatalf("OSSURL(%q, %v) = %q, want %q", c.url, c.args, got, c.want)
		}
	}
}

// TestOSSLocalMode bucket 空 → 本地路径 + 同一参数（imgproc 识别）。
func TestOSSLocalMode(t *testing.T) {
	cases := []struct {
		url  string
		args []any
		want string
	}{
		{"/uploads/a.jpg", nil, "/uploads/a.jpg"},
		{"/uploads/a.jpg", []any{600, 450},
			"/uploads/a.jpg?x-oss-process=image/resize,w_600,h_450,m_fill"},
		{"/uploads/a.jpg", []any{600, 450, "fit"},
			"/uploads/a.jpg?x-oss-process=image/resize,w_600,h_450,m_lfit"},
		{"/uploads/v.mp4", []any{600}, "/uploads/v.mp4"},
	}
	for _, c := range cases {
		got := OSSURL("", c.url, c.args...)
		if got != c.want {
			t.Fatalf("local mode %q %v = %q, want %q", c.url, c.args, got, c.want)
		}
	}
}

// TestRich 富文本内嵌媒体 src 转 oss。
func TestRich(t *testing.T) {
	bucket := "https://viicn-files.oss-cn-beijing.aliyuncs.com"
	html := `<p>正文 <img src="/uploads/a.jpg" alt="x"> <video src="/uploads/v.mp4"></video></p>`
	got := string(Rich(bucket, html))
	if !strings.Contains(got, `src="https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/a.jpg"`) {
		t.Fatalf("img not converted: %q", got)
	}
	if !strings.Contains(got, `src="https://viicn-files.oss-cn-beijing.aliyuncs.com/uploads/v.mp4"`) {
		t.Fatalf("video not converted: %q", got)
	}
	// 外部 URL 不转
	html2 := `<p><img src="https://cdn.example.com/x.jpg"></p>`
	got2 := string(Rich(bucket, html2))
	if strings.Contains(got2, "viicn-files") {
		t.Fatalf("external should not be converted: %q", got2)
	}
	// bucket 空 = 本地原路径
	got3 := string(Rich("", `<img src="/uploads/x.png">`))
	if !strings.Contains(got3, `src="/uploads/x.png"`) {
		t.Fatalf("local mode: %q", got3)
	}
}
