package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kran/gcmv2/core"
)

// JSON 解码上限：超过 1MB 的表单请求 → 413（不是 400/500，也不会把整个 body 读进内存）。
func TestJSONBodyLimit(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.WriteRule(WriteCreate, "guestbook", func(_ *CmsCtx, _ *core.Node, allow *core.List[string]) error {
			allow.Append("title")
			return nil
		})
	})
	oversized := map[string]any{
		"display": "big",
		"fields":  map[string]any{"title": strings.Repeat("x", maxJSONBytes+1)},
	}
	w := do(site, http.MethodPost, "/api/nodes/guestbook", oversized)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized JSON = %d %s", w.Code, w.Body.String())
	}
	if code := errorCode(t, w); code != string(CodeInvalidRequest) {
		t.Fatalf("oversized JSON code = %q", code)
	}
	w = do(site, http.MethodPost, "/api/nodes/guestbook", map[string]any{
		"display": "ok", "fields": map[string]any{"title": "ok"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("normal JSON = %d %s", w.Code, w.Body.String())
	}
}

// 硬上限兜住所有请求体（含不走 JSON 解码的路由与插件路由）。
func TestBodyHardLimit(t *testing.T) {
	site := testSite(t)
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", maxBodyBytes+1)))
	ctx := site.CmsCtxMaker(httptest.NewRecorder(), req)
	if _, err := io.ReadAll(ctx.R.Body); err == nil {
		t.Fatal("hard body limit is not enforced")
	}
}
