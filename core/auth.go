package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/kran/dba"
)

// AuthMethod binds one credential, owned by a credential plugin, to an
// authentication-enabled Node. Core stores credential data opaquely.
type AuthMethod struct {
	ID         int64     `db:"id,omitempty" json:"id"`
	NodeType   string    `db:"type" json:"node_type"`
	NodeID     int64     `db:"node_id" json:"node_id"`
	Method     string    `db:"method" json:"method"`
	Identifier string    `db:"identifier" json:"identifier"`
	Data       Fields    `db:"data" json:"-"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}

// Session is a Realm-bound Node session. TokenHash is never sent to clients.
type Session struct {
	TokenHash string    `db:"token_hash" json:"-"`
	Realm     string    `db:"realm" json:"realm"`
	NodeID    int64     `db:"node_id" json:"node_id"`
	ExpiresAt time.Time `db:"expires_at" json:"expires_at"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// SessionTTL is the sliding frontend session lifetime.
const SessionTTL = 7 * 24 * time.Hour

// RegisterAuth atomically creates an authentication-enabled Node and binds an
// opaque credential to it.
func (s *Service) RegisterAuth(nodeType, method, identifier string, data Fields, n *Node) (int64, error) {
	td, ok := s.types.Type(nodeType)
	if !ok {
		return 0, fmt.Errorf("core: auth: type %q not defined", nodeType)
	}
	if !td.Capabilities.Authentication {
		return 0, fmt.Errorf("core: auth: type %q is not auth-enabled", nodeType)
	}
	if method == "" || identifier == "" {
		return 0, errors.New("core: auth: method and identifier required")
	}
	existing, err := s.FindAuth(nodeType, method, identifier)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		return 0, fmt.Errorf("core: auth: %s %q already registered", method, identifier)
	}
	if n == nil {
		n = &Node{}
	}
	n.Type = nodeType

	var nodeID int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		id, err := s.CreateNode(n)
		if err != nil {
			return err
		}
		nodeID = id
		now := time.Now()
		authMethod := &AuthMethod{
			NodeType: nodeType, NodeID: id, Method: method, Identifier: identifier,
			Data: data, CreatedAt: now, UpdatedAt: now,
		}
		_, err = tx.Insert("auth_methods", authMethod).Exec()
		if err != nil {
			return fmt.Errorf("core: auth: insert method: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return nodeID, nil
}

// FindAuth finds a credential by Node type, method, and identifier.
func (s *Service) FindAuth(nodeType, method, identifier string) (*AuthMethod, error) {
	return s.db.Add(
		`SELECT * FROM auth_methods WHERE type = #{1} AND method = #{2} AND identifier = #{3}`,
		nodeType, method, identifier).FetchOne[AuthMethod]()
}

// AddAuthMethod binds an opaque credential to an existing Node.
func (s *Service) AddAuthMethod(nodeType string, nodeID int64, method, identifier string, data Fields) error {
	if method == "" || identifier == "" {
		return errors.New("core: auth: method and identifier required")
	}
	td, ok := s.types.Type(nodeType)
	if !ok || !td.Capabilities.Authentication {
		return fmt.Errorf("core: auth: type %q is not auth-enabled", nodeType)
	}
	node, err := s.GetNodeById(nodeID)
	if err != nil {
		return err
	}
	if node == nil {
		return ErrNotFound
	}
	if node.Type != nodeType {
		return fmt.Errorf("core: auth: node %d is type %q, not %q", nodeID, node.Type, nodeType)
	}
	existing, err := s.FindAuth(nodeType, method, identifier)
	if err != nil {
		return err
	}
	if existing != nil {
		return fmt.Errorf("core: auth: %s %q already registered", method, identifier)
	}
	now := time.Now()
	authMethod := &AuthMethod{
		NodeType: nodeType, NodeID: nodeID, Method: method, Identifier: identifier,
		Data: data, CreatedAt: now, UpdatedAt: now,
	}
	_, err = s.db.Insert("auth_methods", authMethod).Exec()
	return err
}

// RemoveAuthMethod unbinds a credential while retaining at least one method.
func (s *Service) RemoveAuthMethod(nodeType, method, identifier string) error {
	count, err := s.db.Add(
		`SELECT COUNT(1) FROM auth_methods WHERE type = #{1} AND node_id =
		 (SELECT node_id FROM auth_methods WHERE type = #{1} AND method = #{2} AND identifier = #{3})`,
		nodeType, method, identifier).FetchOne[int64]()
	if err != nil {
		return err
	}
	if count != nil && *count <= 1 {
		return errors.New("core: auth: cannot remove last auth method")
	}
	_, err = s.db.Delete("auth_methods", `type = #{1} AND method = #{2} AND identifier = #{3}`,
		nodeType, method, identifier).Exec()
	return err
}

// CreateSession creates a Realm-bound session and returns the raw bearer token.
// Only its SHA-256 hash is stored.
func (s *Service) CreateSession(realm string, nodeID int64) (string, error) {
	if realm == "" {
		return "", errors.New("core: auth: session realm required")
	}
	node, err := s.GetNodeById(nodeID)
	if err != nil {
		return "", err
	}
	if node == nil {
		return "", ErrNotFound
	}
	td, ok := s.types.Type(node.Type)
	if !ok || !td.Capabilities.Authentication {
		return "", fmt.Errorf("core: auth: node type %q is not auth-enabled", node.Type)
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	session := &Session{
		TokenHash: sessionTokenHash(token), Realm: realm, NodeID: nodeID,
		ExpiresAt: now.Add(SessionTTL), CreatedAt: now,
	}
	_, err = s.db.Insert("sessions", session).Exec()
	if err != nil {
		return "", err
	}
	return token, nil
}

// ValidSession resolves a raw token. Invalid or expired tokens return nil.
func (s *Service) ValidSession(token string) (*Session, error) {
	if token == "" {
		return nil, nil
	}
	hash := sessionTokenHash(token)
	record, err := s.db.Add(`SELECT * FROM sessions WHERE token_hash = #{1}`, hash).FetchOne[Session]()
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil
	}
	if record.ExpiresAt.Before(time.Now()) {
		_, _ = s.db.Delete("sessions", `token_hash = #{1}`, hash).Exec()
		return nil, nil
	}
	if time.Until(record.ExpiresAt) < SessionTTL/2 {
		record.ExpiresAt = time.Now().Add(SessionTTL)
		_, _ = s.db.Update("sessions", dba.H{"expires_at": record.ExpiresAt}, `token_hash = #{1}`, hash).Exec()
	}
	return record, nil
}

// DeleteSession invalidates one raw session token.
func (s *Service) DeleteSession(token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.Delete("sessions", `token_hash = #{1}`, sessionTokenHash(token)).Exec()
	return err
}

// DeleteNodeSessions invalidates every frontend session for a Node.
func (s *Service) DeleteNodeSessions(nodeID int64) error {
	_, err := s.db.Delete("sessions", `node_id = #{1}`, nodeID).Exec()
	return err
}

func sessionTokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
