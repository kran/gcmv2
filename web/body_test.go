package web

import (
	"net/http"
	"testing"

	"github.com/kran/gcmv2/core"
)

// JSON 请求体的大小不在这里限制（部署侧 nginx 的 client_max_body_size 负责）——
// 这里只保"解码语义"：一个 JSON 值、未知字段报错。
func TestStrictJSONDecoding(t *testing.T) {
	site := testSiteConfigured(t, func(site *Site) {
		site.WriteRule(WriteCreate, "guestbook", func(_ *CmsCtx, _ *core.Node, allow *core.List[string]) error {
			allow.Append("title")
			return nil
		})
	})
	w := do(site, http.MethodPost, "/api/nodes/guestbook", map[string]any{
		"display": "ok", "fields": map[string]any{"title": "ok"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("normal JSON = %d %s", w.Code, w.Body.String())
	}
	// 未知字段仍然报错（这是解码语义，不是大小限制）
	w = do(site, http.MethodPost, "/api/nodes/guestbook", map[string]any{
		"display": "ok", "fields": map[string]any{"title": "ok"}, "mystery": 1,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d %s", w.Code, w.Body.String())
	}
}
