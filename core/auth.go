package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/kran/dba"
	"golang.org/x/crypto/bcrypt"
)

// ── 前台用户认证: 多登录方式（auth_methods）+ 会话（sessions） ──
//
// 结构（PB 参考）:
//   - 可认证类型在 types.yaml 声明（auth: true）— 节点存 nodes（资料在 fields）
//   - 认证信息（email/密码 hash）存 auth_methods — 一个节点多行 = 多登录方式
//   - 会话 node 级（sessions）— 任何方式登录进同一会话; token 双轨
//     （cookie 与 Bearer 是同一个字符串 — 两种携带方式）
//   - bcrypt 只在 auth_methods.secret — fields 永不出现密码

// AuthMethod 一种登录方式（一个节点可多行）。
type AuthMethod struct {
	ID         int64     `db:"id,omitempty" json:"id"`
	Type       string    `db:"type" json:"type"`
	NodeID     int64     `db:"node_id" json:"node_id"`
	Method     string    `db:"method" json:"method"`
	Identifier string    `db:"identifier" json:"identifier"`
	Data       Fields    `db:"data" json:"-"` // 凭证 JSON（各方式自定义 — password hash/oauth token...; 永不输出）
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}

// Session 会话（node 级 — 与登录方式无关）。
type Session struct {
	Token     string    `db:"token" json:"-"`
	NodeID    int64     `db:"node_id" json:"node_id"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// SessionTTL 会话有效期（滑动 — 活跃续期, 过期重新登录）。
const SessionTTL = 7 * 24 * time.Hour

// ── 认证方式 ──────────────────────────────────

// RegisterAuth 注册: 事务内建 auth 类型节点 + 认证方式（原子）。
// 返回节点 id。identifier 冲突 → 报错（UNIQUE 兜底 + 提前查重给友好错误）。
func (s *Service) RegisterAuth(typeName, method, identifier string, data Fields, n *Node) (int64, error) {
	td, ok := s.types.Type(typeName)
	if !ok {
		return 0, fmt.Errorf("core: auth: type %q not defined", typeName)
	}
	if !td.Auth {
		return 0, fmt.Errorf("core: auth: type %q is not auth-enabled", typeName)
	}
	if method == "" || identifier == "" {
		return 0, errors.New("core: auth: method and identifier required")
	}
	// 查重（提前 — 友好错误; UNIQUE 兜底并发）
	ex, err := s.FindAuth(typeName, method, identifier)
	if err != nil {
		return 0, err
	}
	if ex != nil {
		return 0, fmt.Errorf("core: auth: %s %q already registered", method, identifier)
	}
	// 类型补全（n.Type 可能为空 — 以 typeName 为准）
	if n == nil {
		n = &Node{}
	}
	n.Type = typeName

	var nodeID int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		// CreateNode 内部事务 — 嵌套检测（pool == nil）直接并入当前事务
		id, err := s.CreateNode(n)
		if err != nil {
			return err
		}
		nodeID = id
		now := time.Now()
		if _, err := tx.Insert("auth_methods", &AuthMethod{
			Type: typeName, NodeID: id, Method: method, Identifier: identifier,
			Data: data, CreatedAt: now, UpdatedAt: now,
		}).Exec(); err != nil {
			return fmt.Errorf("core: auth: insert method: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return nodeID, nil
}

// FindAuth 按 (type, method, identifier) 找认证方式; 未找到返回 (nil, nil)。
func (s *Service) FindAuth(typeName, method, identifier string) (*AuthMethod, error) {
	return s.db.Add(
		`SELECT * FROM auth_methods WHERE type = #{1} AND method = #{2} AND identifier = #{3}`,
		typeName, method, identifier).FetchOne[AuthMethod]()
}

// VerifyPassword 验密（password 方式 — 比较 Data["password"] 与输入; bcrypt）。
func (s *Service) VerifyPassword(am *AuthMethod, secret string) bool {
	if am == nil {
		return false
	}
	hash := am.Data.Str("password")
	if hash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}

// AddAuthMethod 给已有节点加登录方式（bind — 登录后绑定新方式）。
func (s *Service) AddAuthMethod(typeName string, nodeID int64, method, identifier string, data Fields) error {
	if method == "" || identifier == "" {
		return errors.New("core: auth: method and identifier required")
	}
	td, ok := s.types.Type(typeName)
	if !ok || !td.Auth {
		return fmt.Errorf("core: auth: type %q is not auth-enabled", typeName)
	}
	node, err := s.GetNodeById(nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return ErrNotFound
	}
	if node.Type != typeName {
		return fmt.Errorf("core: auth: node %d is type %q, not %q", nodeID, node.Type, typeName)
	}
	ex, err := s.FindAuth(typeName, method, identifier)
	if err != nil {
		return err
	}
	if ex != nil {
		return fmt.Errorf("core: auth: %s %q already registered", method, identifier)
	}
	now := time.Now()
	if _, err := s.db.Insert("auth_methods", &AuthMethod{
		Type: typeName, NodeID: nodeID, Method: method, Identifier: identifier,
		Data: data, CreatedAt: now, UpdatedAt: now,
	}).Exec(); err != nil {
		return err
	}
	return nil
}

// RemoveAuthMethod 解绑登录方式（至少保留一种 — 防锁死）。
func (s *Service) RemoveAuthMethod(typeName, method, identifier string) error {
	n, err := s.db.Add(
		`SELECT COUNT(1) FROM auth_methods WHERE type = #{1} AND node_id =
		 (SELECT node_id FROM auth_methods WHERE type = #{1} AND method = #{2} AND identifier = #{3})`,
		typeName, method, identifier).FetchOne[int64]()
	if err != nil {
		return err
	}
	if n != nil && *n <= 1 {
		return errors.New("core: auth: cannot remove last auth method")
	}
	if _, err := s.db.Delete("auth_methods", `type = #{1} AND method = #{2} AND identifier = #{3}`,
		typeName, method, identifier).Exec(); err != nil {
		return err
	}
	return nil
}

// ── 会话 ──────────────────────────────────────

// CreateSession 建会话（登录成功）; 返回 token（cookie 与 Bearer 同值）。
func (s *Service) CreateSession(nodeID int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	if _, err := s.db.Insert("sessions", &Session{
		Token: token, NodeID: nodeID,
		ExpiresAt: now.Add(SessionTTL), CreatedAt: now,
	}).Exec(); err != nil {
		return "", err
	}
	return token, nil
}

// ValidSession 校验 token → 节点 id; 无效返回 0。
// 滑动过期: 剩余不足一半才更新 expires_at（活跃用户写库频率极低）。
func (s *Service) ValidSession(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	rec, err := s.db.Add(`SELECT * FROM sessions WHERE token = #{1}`, token).FetchOne[Session]()
	if err != nil {
		return 0, err
	}
	if rec == nil {
		return 0, nil
	}
	if rec.ExpiresAt.Before(time.Now()) {
		// 过期 — 清理
		_, _ = s.db.Delete("sessions", `token = #{1}`, token).Exec()
		return 0, nil
	}
	if time.Until(rec.ExpiresAt) < SessionTTL/2 {
		_, _ = s.db.Update("sessions", dba.H{"expires_at": time.Now().Add(SessionTTL)}, `token = #{1}`, token).Exec()
	}
	return rec.NodeID, nil
}

// DeleteSession 登出（删 token — cookie/Bearer 同时失效）。
func (s *Service) DeleteSession(token string) error {
	_, err := s.db.Delete("sessions", `token = #{1}`, token).Exec()
	return err
}

// randomToken 32 字节随机 hex（会话 token）。
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
