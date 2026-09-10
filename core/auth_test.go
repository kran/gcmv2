package core

import (
	"errors"
	"testing"
	"time"
)

// testAuthType user 可认证类型（testTypesYAML 基础上加）。
const testAuthYAML = `
types:
  user:
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
  plain:
    fields:
      - { name: name, kind: text }
`

func newAuthService(t *testing.T) *Service {
	t.Helper()
	svc := newTestService(t)
	if err := svc.types.Load([]byte(testAuthYAML)); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRegisterAuth(t *testing.T) {
	s := newAuthService(t)
	id, err := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"opaque": "credential-data"},
		&Node{Display: "张三", Fields: map[string]any{"name": "张三"}})
	if err != nil {
		t.Fatal(err)
	}
	// 节点存在 + 类型正确
	n, err := s.GetNodeById(t.Context(), id)
	if err != nil || n == nil {
		t.Fatalf("node = %v, %v", n, err)
	}
	if n.Type != "user" {
		t.Fatalf("type = %q", n.Type)
	}
	// Core stores credential data opaquely and keeps it out of JSON.
	am, err := s.FindAuth(t.Context(), "user", "email", "a@x.com")
	if err != nil || am == nil {
		t.Fatalf("auth = %v, %v", am, err)
	}
	if am.NodeType != "user" || am.Data.Str("opaque") != "credential-data" {
		t.Fatalf("auth method = %#v", am)
	}
}

func TestRegisterAuthDupIdentifier(t *testing.T) {
	s := newAuthService(t)
	if _, err := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"}); err == nil {
		t.Fatal("duplicate identifier should fail")
	}
	// 不同类型同 identifier 允许
	if _, err := s.RegisterAuth(t.Context(), "user", "phone", "13800138000", Fields{"credential": "x"}, &Node{Display: "a"}); err != nil {
		t.Fatalf("different method should work: %v", err)
	}
}

func TestRegisterAuthNotAuthType(t *testing.T) {
	s := newAuthService(t)
	if _, err := s.RegisterAuth(t.Context(), "article", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"}); err == nil {
		t.Fatal("non-auth type should be rejected")
	}
}

func TestRegisterAuthWechatNoPassword(t *testing.T) {
	s := newAuthService(t)
	// wechat 方式无密码（data 空/任意）— 可注册
	if _, err := s.RegisterAuth(t.Context(), "user", "wechat", "openid_1", Fields{}, &Node{Display: "a"}); err != nil {
		t.Fatalf("wechat no-password should register: %v", err)
	}
	am, _ := s.FindAuth(t.Context(), "user", "wechat", "openid_1")
	if am == nil || am.Data.Str("password") != "" {
		t.Fatalf("wechat data should be empty password: %+v", am)
	}
}

func TestAddRemoveAuthMethod(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"})
	// 绑定第二种方式
	if err := s.AddAuthMethod(t.Context(), "user", id, "phone", "13800138000", Fields{"credential": "x"}); err != nil {
		t.Fatal(err)
	}
	am, _ := s.FindAuth(t.Context(), "user", "phone", "13800138000")
	if am == nil || am.NodeID != id {
		t.Fatalf("bound method = %+v", am)
	}
	// 解绑（保留一种 — 允许）
	if err := s.RemoveAuthMethod(t.Context(), "user", "phone", "13800138000"); err != nil {
		t.Fatal(err)
	}
	// 解绑最后一种 — 拒绝
	if err := s.RemoveAuthMethod(t.Context(), "user", "email", "a@x.com"); err == nil {
		t.Fatal("removing last method should fail")
	}
}

func TestAddAuthMethodValidatesNodeType(t *testing.T) {
	s := newAuthService(t)
	userID, err := s.CreateNode(t.Context(), &Node{Type: "user", Display: "user", Fields: Fields{"name": "user"}})
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := s.CreateNode(t.Context(), &Node{Type: "plain", Display: "plain", Fields: Fields{"name": "plain"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuthMethod(t.Context(), "staff", userID, "email", "staff@x.com", Fields{}); err == nil {
		t.Fatal("node/type mismatch must fail")
	}
	if err := s.AddAuthMethod(t.Context(), "plain", plainID, "email", "plain@x.com", Fields{}); err == nil {
		t.Fatal("non-auth type must fail")
	}
	if err := s.AddAuthMethod(t.Context(), "user", 999999, "email", "missing@x.com", Fields{}); err != ErrNotFound {
		t.Fatalf("missing node error = %v, want ErrNotFound", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"})
	token, err := s.CreateSession(t.Context(), "members", id)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("token empty")
	}
	// 有效
	got, err := s.ValidSession(t.Context(), token)
	if err != nil || got == nil || got.NodeID != id || got.Realm != "members" {
		t.Fatalf("valid session = %#v, %v", got, err)
	}
	stored, err := s.db.Add(`SELECT token_hash FROM sessions WHERE node_id = #{1}`, id).FetchOne[string]()
	if err != nil || stored == nil || *stored == token {
		t.Fatalf("stored token must be hashed: value=%v err=%v", stored, err)
	}
	// 无效 token
	got, _ = s.ValidSession(t.Context(), "nope")
	if got != nil {
		t.Fatalf("invalid token should fail: %#v", got)
	}
	// 登出
	if err := s.DeleteSession(t.Context(), token); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ValidSession(t.Context(), token)
	if got != nil {
		t.Fatal("deleted session should be invalid")
	}
}

func TestDeleteNodeSessions(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"})
	first, _ := s.CreateSession(t.Context(), "members", id)
	second, _ := s.CreateSession(t.Context(), "members", id)
	if err := s.DeleteNodeSessions(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{first, second} {
		session, err := s.ValidSession(t.Context(), token)
		if err != nil || session != nil {
			t.Fatalf("session after revoke = %#v, %v", session, err)
		}
	}
}

func TestArchiveAuthNodeRevokesSessions(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"})
	token, _ := s.CreateSession(t.Context(), "members", id)
	node, _ := s.GetNodeById(t.Context(), id)
	if err := s.ArchiveNode(t.Context(), id, node.Revision); err != nil {
		t.Fatal(err)
	}
	session, err := s.ValidSession(t.Context(), token)
	if err != nil || session != nil {
		t.Fatalf("archived node session = %#v, %v", session, err)
	}
	if _, err := s.CreateSession(t.Context(), "members", id); !errors.Is(err, ErrNodeArchived) {
		t.Fatalf("session for archived node = %v", err)
	}
	archived, _ := s.GetNodeById(t.Context(), id)
	if err := s.RestoreNode(t.Context(), id, archived.Revision); err != nil {
		t.Fatal(err)
	}
	restored, _ := s.GetNodeById(t.Context(), id)
	if restored.ArchivedAt != nil {
		t.Fatalf("restored node = %#v", restored)
	}
}

func TestSessionExpiry(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth(t.Context(), "user", "email", "a@x.com", Fields{"credential": "x"}, &Node{Display: "a"})
	token, _ := s.CreateSession(t.Context(), "members", id)
	// 手动过期
	_, err := s.db.Update("sessions", map[string]any{"expires_at": time.Now().Add(-time.Hour)},
		`token_hash = #{1}`, sessionTokenHash(token)).Exec()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.ValidSession(t.Context(), token)
	if got != nil {
		t.Fatal("expired session should be invalid")
	}
	// 过期清理（行删除）
	n, err := s.db.Add(`SELECT COUNT(1) FROM sessions WHERE token_hash = #{1}`, sessionTokenHash(token)).FetchOne[int64]()
	if err != nil {
		t.Fatal(err)
	}
	if n == nil || *n != 0 {
		t.Fatal("expired session row should be cleaned")
	}
}
