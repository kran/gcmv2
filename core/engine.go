package core

import (
	"context"

	"github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
)

// Engine 引擎对外契约（门面 — 消费方依赖此接口, 实现是 Service）。
//
// 设计原则（参考 PocketBase App 门面, 但控制在 ~25 方法）:
//   - 接口与实现同包（core）— 依赖方向朝下, 循环依赖从结构上不可能
//   - 只含"被消费"的能力: 写（hook 挂扩展）/ 读 / 图原语 / 容器
//   - 内部方法（addEdges/splitFields/buildQuery 等）不进接口 — 接口 = 对外契约
//   - auth 等未来能力: 接口向后扩展（实现补齐即可）, 不在现阶段占位
type Engine interface {
	// ── 写（hook 挂扩展点 — BeforeCreate/AfterCreate/...） ──
	CreateNode(n *Node) (int64, error)
	PatchNode(id int64, patch *NodePatch) error
	DeleteNode(id int64) error

	// ── 读 ──
	QueryPage(ctx context.Context, q ListQuery) ([]Node, int64, error)
	Query(ctx context.Context, q ListQuery) ([]Node, error)
	GetNodeById(id int64) (*Node, error)
	GetNodeByAddress(address string) (*Node, error)
	FullFields(id int64) (map[string]any, error)

	// ── 图原语 ──
	LoadTree(typeName string) (*Tree, error)
	Subtree(typeName string, start int64, field string, maxHops int) ([]int64, error)
	Ancestors(typeName string, start int64, field string, maxHops int) ([]*Node, error)
	Traverse(typeName string, start int64, field string, maxHops int) ([]int64, error)
	OutEdges(typeName string, from int64, field string, page, size int) ([]Edge, int64, error)
	InEdges(to int64, field string, page, size int) ([]Edge, int64, error)

	// ── 搜索/展开 ──
	Search(q, typ string, page, size int) ([]Node, int64, error)
	RebuildSearch() error
	EquivalenceClass(typeName string, start int64, field string, maxHops int) ([]int64, error)
	Expand(ctx context.Context, id int64, paths ...query.ExpandPath) (*Node, error)
	ExpandMany(ctx context.Context, ids []int64, paths ...query.ExpandPath) ([]*Node, error)
	AutoExpand(typeName string) []query.ExpandPath

	// ── 认证（前台用户 — 多登录方式 + 会话） ──
	RegisterAuth(typeName, method, identifier string, data Fields, n *Node) (int64, error)
	FindAuth(typeName, method, identifier string) (*AuthMethod, error)
	VerifyPassword(am *AuthMethod, secret string) bool
	AddAuthMethod(typeName string, nodeID int64, method, identifier string, data Fields) error
	RemoveAuthMethod(typeName, method, identifier string) error
	CreateSession(nodeID int64) (string, error)
	ValidSession(token string) (int64, error)
	DeleteSession(token string) error

	// ── 容器 ──
	Types() *types.Types
	Hooks() *HookBus
	GetSetting(key string) (*Setting, error)
	SetSetting(key, group, typ string, value any) error
	ListSettings(group string) ([]Setting, error)
	DeleteSetting(key string) error

	// others
	Migrator() *Migrator
}

// ── hook 事件名（对称命名 — 写路径扩展点） ──
const (
	HookNodeBeforeCreate = "node.before_create" // 新建前（事务内, 失败回滚 — 审计/默认值）
	HookNodeAfterCreate  = "node.after_create"  // 新建后（事务内 — 搜索同步/通知）
	HookNodeBeforeUpdate = "node.before_update" // 更新前（事务内 — 审计/级联检查）
	HookNodeAfterUpdate  = "node.after_update"  // 更新后（事务内 — 搜索同步/通知）
	HookNodeBeforeDelete = "node.before_delete" // 删除前（事务内 — 级联检查/拒绝删除）
	HookNodeAfterDelete  = "node.after_delete"  // 删除后（事务内 — 搜索删除/审计）
)

// Engine 编译期断言: Service 实现完整契约（接口方法增减 → 此处编译报错）。
var _ Engine = (*Service)(nil)
