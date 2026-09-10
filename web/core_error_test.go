package web

import (
	"net/http"
	"testing"

	"github.com/kran/gcmv2/core"
)

// TestPatchWithoutRevisionIsClientError 漏 revision 是客户端错误（4xx），不是 500 —
// 后台面板省略 revision 时不该表现为服务端故障。
func TestPatchWithoutRevisionIsClientError(t *testing.T) {
	s := testSite(t)
	ctx := t.Context()
	id, err := s.Engine().CreateNode(ctx, &core.Node{
		Type: "article", Display: "x", Fields: core.Fields{"publication_state": "published"},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.Engine().PatchNode(ctx, id, &core.NodePatch{Display: ptr("y")})
	if err == nil {
		t.Fatal("want error without revision")
	}
	mapped := CoreError(err)
	if mapped == nil {
		t.Fatalf("CoreError(%v) = nil, want 422", err)
	}
	if mapped.Status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", mapped.Status)
	}
}

func ptr[T any](v T) *T { return &v }
