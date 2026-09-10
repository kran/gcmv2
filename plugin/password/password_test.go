package password

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kran/gcmv2/web"
)

func newSite(t *testing.T) *web.Site {
	t.Helper()
	dir := t.TempDir()
	typesYAML := `
types:
  member:
    capabilities:
      authentication: true
    fields:
      - { name: name, kind: text }
      - { name: role, kind: select, options: [member, editor] }
  staff:
    capabilities:
      authentication: true
    fields:
      - { name: name, kind: text }
`
	typesPath := filepath.Join(dir, "types.yaml")
	err := os.WriteFile(typesPath, []byte(typesYAML), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.MkdirAll(filepath.Join(dir, "templates"), 0o755)
	if err != nil {
		t.Fatal(err)
	}
	site := web.New(dir)
	site.Auth().Register(web.AuthRealm{
		Name: "members", NodeType: "member", AllowRegister: true, Default: true,
	})
	site.Auth().Register(web.AuthRealm{Name: "staff", NodeType: "staff"})
	Mount(site, Options{Realm: "members"})
	Mount(site, Options{Realm: "staff"})
	site.Start()
	return site
}

func post(site *web.Site, path string, body map[string]any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	site.Handler().ServeHTTP(response, request)
	return response
}

func TestPasswordRegisterLoginBind(t *testing.T) {
	site := newSite(t)
	response := post(site, "/api/auth/members/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
		"display": "张三", "fields": map[string]any{"name": "张三"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("register = %d: %s", response.Code, response.Body.String())
	}
	var output struct {
		Token string `json:"token"`
		Actor struct {
			Realm string `json:"realm"`
		} `json:"actor"`
		User struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"user"`
	}
	err := json.Unmarshal(response.Body.Bytes(), &output)
	if err != nil {
		t.Fatal(err)
	}
	if output.Token == "" || output.User.ID == 0 || output.User.Type != "member" || output.Actor.Realm != "members" {
		t.Fatalf("bad register response: %+v", output)
	}

	response = post(site, "/api/auth/members/login", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123",
	})
	if response.Code != http.StatusOK {
		t.Fatalf("login = %d: %s", response.Code, response.Body.String())
	}
	response = post(site, "/api/auth/members/login", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "wrong",
	})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("login(wrong) = %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+output.Token)
	context := site.CmsCtxMaker(httptest.NewRecorder(), request)
	actor := context.Actor()
	if actor.Kind != web.ActorNode || actor.NodeID != output.User.ID {
		t.Fatalf("actor = %#v", actor)
	}

	cookie := &http.Cookie{Name: "gcm_auth", Value: output.Token}
	response = post(site, "/api/auth/members/bind", map[string]any{
		"method": "phone", "identifier": "13800138000", "secret": "password456",
	}, cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("bind = %d: %s", response.Code, response.Body.String())
	}
	method, err := site.Engine().FindAuth(t.Context(), "member", "phone", "13800138000")
	if err != nil || method == nil || method.NodeID != output.User.ID {
		t.Fatalf("bound method = %#v, %v", method, err)
	}
	response = post(site, "/api/auth/staff/bind", map[string]any{
		"method": "phone", "identifier": "13900139000", "secret": "password789",
	}, cookie)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-realm bind = %d: %s", response.Code, response.Body.String())
	}
}

func TestPasswordRejectsClientNodeType(t *testing.T) {
	site := newSite(t)
	legacy := post(site, "/api/auth/register", map[string]any{
		"method": "email", "identifier": "legacy@x.com", "secret": "password123", "display": "legacy",
	})
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy route = %d: %s", legacy.Code, legacy.Body.String())
	}
	response := post(site, "/api/auth/members/register", map[string]any{
		"type": "admin", "method": "email", "identifier": "a@x.com",
		"secret": "password123", "display": "a",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("register with type = %d: %s", response.Code, response.Body.String())
	}
}

func TestPasswordRejectsForeignCredentialMethod(t *testing.T) {
	site := newSite(t)
	response := post(site, "/api/auth/members/register", map[string]any{
		"method": "wechat", "identifier": "openid", "secret": "password123", "display": "attacker",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("foreign method = %d: %s", response.Code, response.Body.String())
	}
}

func TestPasswordRegistrationCanBeDisabledPerRealm(t *testing.T) {
	site := newSite(t)
	response := post(site, "/api/auth/staff/register", map[string]any{
		"method": "email", "identifier": "staff@x.com", "secret": "password123", "display": "staff",
	})
	if response.Code != http.StatusForbidden {
		t.Fatalf("disabled register = %d: %s", response.Code, response.Body.String())
	}
}

func TestPasswordRegisterDuplicate(t *testing.T) {
	site := newSite(t)
	post(site, "/api/auth/members/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password123", "display": "a",
	})
	response := post(site, "/api/auth/members/register", map[string]any{
		"method": "email", "identifier": "a@x.com", "secret": "password456", "display": "a",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate register = %d", response.Code)
	}
}
