package highlight

import (
	"strings"
	"testing"
)

func TestHighlight(t *testing.T) {
	got := string(Highlight("北京志起未来咨询集团", "志起"))
	if !strings.Contains(got, "<mark>志起</mark>") {
		t.Fatalf("got %q", got)
	}
}

func TestHighlightMultiTerm(t *testing.T) {
	got := string(Highlight("北京CBD 上海陆家嘴", "北京 上海"))
	if !strings.Contains(got, "<mark>北京</mark>") || !strings.Contains(got, "<mark>上海</mark>") {
		t.Fatalf("got %q", got)
	}
}

func TestHighlightCaseInsensitive(t *testing.T) {
	got := string(Highlight("Hello World", "hello"))
	if !strings.Contains(got, "<mark>Hello</mark>") {
		t.Fatalf("got %q", got)
	}
}

func TestHighlightXSS(t *testing.T) {
	got := string(Highlight(`<script>alert(1)</script>正文`, "正文"))
	if strings.Contains(got, "<script>") {
		t.Fatalf("XSS: %q", got)
	}
	if !strings.Contains(got, "<mark>正文</mark>") {
		t.Fatalf("got %q", got)
	}
}

func TestHighlightEmpty(t *testing.T) {
	if got := string(Highlight("原文", "")); got != "原文" {
		t.Fatalf("got %q", got)
	}
	if got := string(Highlight("", "关键词")); got != "" {
		t.Fatalf("got %q", got)
	}
}
