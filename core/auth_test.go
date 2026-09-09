package core

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
	"time"
)

// testAuthType user 可认证类型（testTypesYAML 基础上加）。
const testAuthYAML = `
types:
  user:
    auth: true
    fields:
      - { name: name, kind: text }
      - { name: role, kind: select, options: [member, editor] }
  staff:
    auth: true
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
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	id, err := s.RegisterAuth("user", "email", "a@x.com", Fields{"password": string(hash)},
		&Node{Display: "张三", Fields: map[string]any{"name": "张三"}})
	if err != nil {
		t.Fatal(err)
	}
	// 节点存在 + 类型正确
	n, err := s.GetNodeById(id)
	if err != nil || n == nil {
		t.Fatalf("node = %v, %v", n, err)
	}
	if n.Type != "user" {
		t.Fatalf("type = %q", n.Type)
	}
	// auth 方式可查 + 验密
	am, err := s.FindAuth("user", "email", "a@x.com")
	if err != nil || am == nil {
		t.Fatalf("auth = %v, %v", am, err)
	}
	if !s.VerifyPassword(am, "password123") {
		t.Fatal("password should match")
	}
	if s.VerifyPassword(am, "wrong") {
		t.Fatal("wrong password should not match")
	}
	// secret 列不可 JSON 输出（json:"-"）— 字段检查
	if am.Data.Str("password") == "password123" {
		t.Fatal("password must be hashed")
	}
}

func TestRegisterAuthDupIdentifier(t *testing.T) {
	s := newAuthService(t)
	if _, err := s.RegisterAuth("user", "email", "a@x.com", Fields{"password": "x"}, &Node{Display: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterAuth("user", "email", "a@x.com", Fields{"password": "x"}, &Node{Display: "a"}); err == nil {
		t.Fatal("duplicate identifier should fail")
	}
	// 不同类型同 identifier 允许
	if _, err := s.RegisterAuth("user", "phone", "13800138000", Fields{"password": "x"}, &Node{Display: "a"}); err != nil {
		t.Fatalf("different method should work: %v", err)
	}
}

func TestRegisterAuthNotAuthType(t *testing.T) {
	s := newAuthService(t)
	if _, err := s.RegisterAuth("article", "email", "a@x.com", Fields{"password": "x"}, &Node{Display: "a"}); err == nil {
		t.Fatal("non-auth type should be rejected")
	}
}

func TestRegisterAuthWechatNoPassword(t *testing.T) {
	s := newAuthService(t)
	// wechat 方式无密码（data 空/任意）— 可注册
	if _, err := s.RegisterAuth("user", "wechat", "openid_1", Fields{}, &Node{Display: "a"}); err != nil {
		t.Fatalf("wechat no-password should register: %v", err)
	}
	am, _ := s.FindAuth("user", "wechat", "openid_1")
	if am == nil || am.Data.Str("password") != "" {
		t.Fatalf("wechat data should be empty password: %+v", am)
	}
}

func TestAddRemoveAuthMethod(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth("user", "email", "a@x.com", Fields{"password": "x"}, &Node{Display: "a"})
	// 绑定第二种方式
	if err := s.AddAuthMethod("user", id, "phone", "13800138000", Fields{"password": "x"}); err != nil {
		t.Fatal(err)
	}
	am, _ := s.FindAuth("user", "phone", "13800138000")
	if am == nil || am.NodeID != id {
		t.Fatalf("bound method = %+v", am)
	}
	// 解绑（保留一种 — 允许）
	if err := s.RemoveAuthMethod("user", "phone", "13800138000"); err != nil {
		t.Fatal(err)
	}
	// 解绑最后一种 — 拒绝
	if err := s.RemoveAuthMethod("user", "email", "a@x.com"); err == nil {
		t.Fatal("removing last method should fail")
	}
}

func TestAddAuthMethodValidatesNodeType(t *testing.T) {
	s := newAuthService(t)
	userID, err := s.CreateNode(&Node{Type: "user", Display: "user", Fields: Fields{"name": "user"}})
	if err != nil {
		t.Fatal(err)
	}
	plainID, err := s.CreateNode(&Node{Type: "plain", Display: "plain", Fields: Fields{"name": "plain"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddAuthMethod("staff", userID, "email", "staff@x.com", Fields{}); err == nil {
		t.Fatal("node/type mismatch must fail")
	}
	if err := s.AddAuthMethod("plain", plainID, "email", "plain@x.com", Fields{}); err == nil {
		t.Fatal("non-auth type must fail")
	}
	if err := s.AddAuthMethod("user", 999999, "email", "missing@x.com", Fields{}); err != ErrNotFound {
		t.Fatalf("missing node error = %v, want ErrNotFound", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth("user", "email", "a@x.com", Fields{"password": "x"}, &Node{Display: "a"})
	token, err := s.CreateSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("token empty")
	}
	// 有效
	got, err := s.ValidSession(token)
	if err != nil || got != id {
		t.Fatalf("valid session = %d, %v", got, err)
	}
	// 无效 token
	got, _ = s.ValidSession("nope")
	if got != 0 {
		t.Fatalf("invalid token should fail: %d", got)
	}
	// 登出
	if err := s.DeleteSession(token); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ValidSession(token)
	if got != 0 {
		t.Fatal("deleted session should be invalid")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := newAuthService(t)
	id, _ := s.RegisterAuth("user", "email", "a@x.com", Fields{"password": "x"}, &Node{Display: "a"})
	token, _ := s.CreateSession(id)
	// 手动过期
	if _, err := s.db.Update("sessions", map[string]any{"expires_at": time.Now().Add(-time.Hour)},
		`token = #{1}`, token).Exec(); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ValidSession(token)
	if got != 0 {
		t.Fatal("expired session should be invalid")
	}
	// 过期清理（行删除）
	n, err := s.db.Add(`SELECT COUNT(1) FROM sessions WHERE token = #{1}`, token).FetchOne[int64]()
	if err != nil {
		t.Fatal(err)
	}
	if n == nil || *n != 0 {
		t.Fatal("expired session row should be cleaned")
	}
}
