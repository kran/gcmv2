package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/gcmv2/core"
)

// TestApiNodesList 列表 + 类型过滤 + 分页。
func TestApiNodesList(t *testing.T) {
	s := testSite(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Engine().CreateNode(t.Context(), &core.Node{
			Type: "article", Display: "文章",
			Fields: map[string]any{"body": "内容", "publication_state": "published"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "草稿",
		Fields: map[string]any{"body": "不可公开", "publication_state": "draft"},
	}); err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/api/nodes/article?size=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("api = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Items []core.Node `json:"items"`
		Total int64       `json:"total"`
		Page  int         `json:"page"`
		Size  int         `json:"size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 || len(out.Items) != 2 {
		t.Fatalf("items=%d total=%d", len(out.Items), out.Total)
	}
}

func TestApiNodesRejectsPublicFilterAndExpand(t *testing.T) {
	s := testSite(t)
	for _, query := range []string{
		"filter=" + url.QueryEscape(`(= $body "x")`),
		"expand=author",
	} {
		w := do(s, "GET", "/api/nodes/article?"+query, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("public query %q = %d, want 400", query, w.Code)
		}
	}
}

func TestApiNodesSort(t *testing.T) {
	s := testSite(t)
	for _, display := range []string{"甲", "乙"} {
		_, err := s.Engine().CreateNode(t.Context(), &core.Node{
			Type: "article", Display: display,
			Fields: map[string]any{"body": display, "publication_state": "published"},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := do(s, "GET", "/api/nodes/article?sort=-id", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("safe sort = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Items []core.Node `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || out.Items[0].ID < out.Items[1].ID {
		t.Fatalf("sort order = %#v", out.Items)
	}
	for _, sort := range []string{"id DESC", "id;DELETE FROM nodes", "$missing"} {
		w := do(s, "GET", "/api/nodes/article?sort="+url.QueryEscape(sort), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unsafe sort %q = %d", sort, w.Code)
		}
	}
}

// TestApiNodesUnknownType 未知类型 → 404 not_found。
func TestApiNodesUnknownType(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/nonexistent", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown type = %d", w.Code)
	}
	if code := errorCode(t, w); code != string(CodeNotFound) {
		t.Fatalf("code = %q", code)
	}
}

func TestApiNodeTypeMismatch(t *testing.T) {
	s := testSite(t)
	id, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "article", Display: "文章",
		Fields: map[string]any{"body": "内容", "publication_state": "published"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct {
		method string
		body   any
	}{
		{http.MethodGet, nil},
		{http.MethodPut, map[string]any{"display": "越界"}},
		{http.MethodDelete, nil},
	} {
		w := do(s, request.method, "/api/nodes/guestbook/"+itoa(id), request.body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s mismatched type = %d, want 404", request.method, w.Code)
		}
	}
}

func TestAPIUploadRequiresLoginAndValidContent(t *testing.T) {
	s := testSite(t)
	w := do(s, http.MethodPost, "/api/upload", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous upload = %d, want 401", w.Code)
	}

	userID, err := s.Engine().CreateNode(t.Context(), &core.Node{
		Type: "user", Display: "上传用户",
		Fields: map[string]any{"name": "上传用户", "role": "member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Engine().CreateSession(t.Context(), "members", userID)
	if err != nil {
		t.Fatal(err)
	}

	validPNG := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}
	w = uploadRequest(t, s, token, "CON?.png", validPNG)
	if w.Code != http.StatusOK {
		t.Fatalf("valid upload = %d: %s", w.Code, w.Body.String())
	}
	var uploaded struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	if uploaded.Name == "" || strings.ContainsAny(uploaded.Name, `?\\/`) {
		t.Fatalf("unsafe uploaded name %q", uploaded.Name)
	}
	if _, err := os.Stat(filepath.Join(s.uploadsDir, uploaded.Name)); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}

	w = uploadRequest(t, s, token, "fake.png", []byte("not a png"))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("mismatched upload = %d, want 422", w.Code)
	}
	if code := errorCode(t, w); code != string(CodeUploadInvalid) {
		t.Fatalf("upload code = %q", code)
	}
}

func uploadRequest(t *testing.T, site *Site, token, name string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	site.Handler().ServeHTTP(response, req)
	return response
}

func TestAPIRejectsLegacyNodeColumns(t *testing.T) {
	s := testSite(t)
	w := do(s, http.MethodPost, "/api/nodes/article", map[string]any{
		"display": "legacy", "status": 1,
		"fields": map[string]any{"body": "x"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("legacy create property = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestApiNodesMissingType 缺 type 段 → 路由 404。
func TestApiNodesMissingType(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing type = %d", w.Code)
	}
}

// TestApiTree 公开树: 只返回 Policy 范围内节点, 未知类型直接拒绝。
func TestApiTree(t *testing.T) {
	s := testSiteWithTemplates(t)
	root, err := s.Engine().CreateNode(t.Context(), &core.Node{Type: "category", Display: "行业",
		Fields: core.Fields{"name": "行业", "publication_state": "published"}})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.Engine().CreateNode(t.Context(), &core.Node{Type: "category", Display: "制造",
		Fields: core.Fields{"name": "制造", "publication_state": "published", "parent": root}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Engine().CreateNode(t.Context(), &core.Node{Type: "category", Display: "草稿",
		Fields: core.Fields{"name": "草稿", "publication_state": "draft", "parent": child}}); err != nil {
		t.Fatal(err)
	}

	w := do(s, "GET", "/api/tree/category", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("tree = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Items []core.TreeNode `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != root {
		t.Fatalf("roots = %+v", out.Items)
	}
	if len(out.Items[0].Children) != 1 || out.Items[0].Children[0].ID != child {
		t.Fatalf("children = %+v", out.Items[0].Children)
	}
	if len(out.Items[0].Children[0].Children) != 0 {
		t.Fatalf("draft node leaked into tree: %+v", out.Items[0].Children[0].Children)
	}

	if w := do(s, "GET", "/api/tree/ghost", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown tree type = %d, want 404", w.Code)
	}
}

// TestHealthEndpoints 运维探针: 存活不碰数据库, 就绪检查连接池并在 Close 后转 503。
func TestHealthEndpoints(t *testing.T) {
	s := testSite(t)
	if w := do(s, "GET", "/healthz", nil); w.Code != http.StatusOK {
		t.Fatalf("healthz = %d", w.Code)
	}
	if w := do(s, "GET", "/readyz", nil); w.Code != http.StatusOK {
		t.Fatalf("readyz = %d", w.Code)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if w := do(s, "GET", "/readyz", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz after close = %d, want 503", w.Code)
	}
	if w := do(s, "GET", "/healthz", nil); w.Code != http.StatusOK {
		t.Fatalf("healthz after close = %d, want 200", w.Code)
	}
	// Close 幂等
	if err := s.Close(); err != nil {
		t.Fatalf("second close = %v", err)
	}
}

// errorCode 取结构化错误响应里的稳定 Code。
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body %q: %v", w.Body.String(), err)
	}
	if body.Error == "" {
		t.Fatalf("error body missing message: %q", w.Body.String())
	}
	return body.Code
}

// TestErrorContract 固定 401/403/404/409/422 的语义与稳定 Code。
func TestErrorContract(t *testing.T) {
	s := testSiteConfigured(t, func(site *Site) {
		// 放行写入, 让后续断言落在数据契约上而不是默认拒绝上。
		site.Hook(HookBeforeCreate, func(*CmsCtx, *core.Node) error { return nil })
		site.Hook(HookBeforeUpdate, func(*CmsCtx, int64, *core.NodePatch) error { return nil })
	})
	// 401: 未认证
	w := do(s, "GET", "/api/auth/me", nil)
	if w.Code != http.StatusUnauthorized || errorCode(t, w) != string(CodeUnauthorized) {
		t.Fatalf("unauthorized = %d %s", w.Code, w.Body.String())
	}
	// 404: 未知类型
	w = do(s, "GET", "/api/nodes/ghost/1", nil)
	if w.Code != http.StatusNotFound || errorCode(t, w) != string(CodeNotFound) {
		t.Fatalf("not found = %d %s", w.Code, w.Body.String())
	}
	// 400: 请求体格式错误
	w = do(s, "POST", "/api/nodes/article", "not json")
	if w.Code != http.StatusBadRequest || errorCode(t, w) != string(CodeInvalidRequest) {
		t.Fatalf("invalid request = %d %s", w.Code, w.Body.String())
	}
	// 422: display 缺失
	w = do(s, "POST", "/api/nodes/article", map[string]any{"display": ""})
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != string(CodeInvalidValue) {
		t.Fatalf("invalid value = %d %s", w.Code, w.Body.String())
	}
	// 422: Schema 校验失败（select 值不在 options 内）
	w = do(s, "POST", "/api/nodes/article", map[string]any{
		"display": "草稿", "fields": map[string]any{"publication_state": "ghost"},
	})
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != string(CodeInvalidValue) {
		t.Fatalf("schema violation = %d %s", w.Code, w.Body.String())
	}
	// 409: 版本冲突
	w = do(s, "POST", "/api/nodes/article", map[string]any{
		"display": "文章", "fields": map[string]any{"body": "x"},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	decodeBody(t, w, &created)
	w = do(s, "PUT", "/api/nodes/article/"+strconv.FormatInt(created.ID, 10), map[string]any{
		"revision": 99, "fields": map[string]any{"body": "y"},
	})
	if w.Code != http.StatusConflict || errorCode(t, w) != string(CodeConflict) {
		t.Fatalf("conflict = %d %s", w.Code, w.Body.String())
	}
}

// TestHookRejectionCarriesStructuredCode Hook 可以用 *web.Error 精确表达语义。
func TestHookRejectionCarriesStructuredCode(t *testing.T) {
	s := testSiteConfigured(t, func(site *Site) {
		site.Hook(HookBeforeCreate, func(*CmsCtx, *core.Node) error {
			return InvalidFields(map[string]string{"title": "标题已存在"})
		})
	})
	w := do(s, "POST", "/api/nodes/article", map[string]any{"display": "文章"})
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hook rejection = %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Code    string            `json:"code"`
		Details map[string]string `json:"details"`
	}
	decodeBody(t, w, &body)
	if body.Code != string(CodeInvalidValue) || body.Details["title"] != "标题已存在" {
		t.Fatalf("hook payload = %#v", body)
	}
}

// decodeBody 解析响应体（失败即测试失败）。
func decodeBody(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
}
