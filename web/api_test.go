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
	"strings"
	"testing"

	"github.com/kran/gcmv2/core"
)

// TestApiNodesList 列表 + 类型过滤 + 分页。
func TestApiNodesList(t *testing.T) {
	s := testSite(t)
	for i := 0; i < 3; i++ {
		if _, err := s.Engine().CreateNode(&core.Node{
			Type: "article", Display: "文章", Status: 1,
			Fields: map[string]any{"body": "内容"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Engine().CreateNode(&core.Node{
		Type: "article", Display: "草稿", Status: 0,
		Fields: map[string]any{"body": "不可公开"},
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
		_, err := s.Engine().CreateNode(&core.Node{
			Type: "article", Display: display, Status: 1,
			Fields: map[string]any{"body": display},
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

// TestApiNodesUnknownType 未知类型 → 400。
func TestApiNodesUnknownType(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/nonexistent", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown type = %d", w.Code)
	}
}

func TestApiNodeTypeMismatch(t *testing.T) {
	s := testSite(t)
	id, err := s.Engine().CreateNode(&core.Node{
		Type: "article", Display: "文章", Status: 1,
		Fields: map[string]any{"body": "内容"},
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

	userID, err := s.Engine().CreateNode(&core.Node{
		Type: "user", Display: "上传用户", Status: core.StatusPublished,
		Fields: map[string]any{"name": "上传用户", "role": "member"},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.Engine().CreateSession(userID)
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
	if w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched upload = %d, want 400", w.Code)
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

// TestApiNodesMissingType 缺 type 段 → 路由 404。
func TestApiNodesMissingType(t *testing.T) {
	s := testSite(t)
	w := do(s, "GET", "/api/nodes/", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing type = %d", w.Code)
	}
}
