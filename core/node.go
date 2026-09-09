package core

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	"github.com/kran/gcmv2/types"
	"github.com/spf13/cast"
)

// ── Node 通用列 ───────────────────────────────
//
// 类型字段名不得与这些保留名冲突（types 校验期拒绝）:
//   id / type / display / revision / fields / created_at / updated_at / archived_at

// Node 节点 — 值模型（读/模板/JSON 展示用）。
//
// Display 是所有实体统一的可读标签；slug、发布状态和人工排序均由类型字段
// 与 capability 声明，不再是 Node 固定语义。
//
// 写路径差量使用 NodePatch：
//
//	CreateNode(n *Node)  全量插入
//	PatchNode(id, patch) 非 nil 列写 + fields json_patch merge
//
// Fields 类型字段（动态 — 类型定义声明; ref 引用在 edges, 不在此）。
// Scan/Value: DB JSON 字符串 ↔ map 自动转换（dba 扫/插直接可用）。
//
// ── cast 快捷方法（容错取值 — 字段值类型多变: JSON float64/int64/string） ──
type Fields map[string]any

// Str 取字符串（nil→""; 数字→字符串）。
func (f Fields) Str(name string) string { return cast.ToString(f[name]) }

// Int 取整数（字符串/float64→int64; 非法→0）。
func (f Fields) Int(name string) int64 { return cast.ToInt64(f[name]) }

// Float 取浮点。
func (f Fields) Float(name string) float64 { return cast.ToFloat64(f[name]) }

// Bool 取布尔。
func (f Fields) Bool(name string) bool { return cast.ToBool(f[name]) }

// Has 字段是否存在（含 null 值）。
func (f Fields) Has(name string) bool { _, ok := f[name]; return ok }

// Map 取嵌套 map。
func (f Fields) Map(name string) map[string]any { return cast.ToStringMap(f[name]) }

// Slice 取数组（nil→空切片）。
func (f Fields) Slice(name string) []any { return cast.ToSlice(f[name]) }

// Scan 从 DB JSON 还原。
func (f *Fields) Scan(v any) error {
	if v == nil {
		*f = Fields{}
		return nil
	}
	var b []byte
	switch t := v.(type) {
	case []byte:
		b = t
	case string:
		b = []byte(t)
	default:
		return fmt.Errorf("core: fields scan: unexpected type %T", v)
	}
	m := Fields{}
	if len(b) > 0 && string(b) != "null" {
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("core: fields scan: %w", err)
		}
	}
	*f = m
	return nil
}

// Value 存库为 JSON。
func (f Fields) Value() (driver.Value, error) {
	if f == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(f)
}

// Node 节点 — 值模型（读/模板/JSON/DB 直接可用）。
type Node struct {
	ID         int64      `db:"id,omitempty" json:"id"` // omitempty: 插入跳零值走自增
	Type       string     `db:"type" json:"type"`
	Display    string     `db:"display" json:"display"`
	Revision   int64      `db:"revision" json:"revision"`
	CreatedAt  time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `db:"updated_at" json:"updated_at"`
	ArchivedAt *time.Time `db:"archived_at" json:"archived_at,omitempty"`

	// 类型字段（Scan/Value 自动 JSON 转换；ref/ref[] 存 edges）
	Fields Fields `db:"fields" json:"fields"`

	// Expand 引用展开容器（ExpandPath 填充 — 不落库）: map[字段名] → *Node / []*Node
	Expand map[string]any `db:"-" json:"expand,omitempty"`
	// Extra 渲染期附加数据（HookNodeEnrich 填充 — 不落库）: url 注入、高亮等
	Extra map[string]any `db:"-" json:"extra,omitempty"`
}

// Field 类型字段值（无 → nil）。
func (n *Node) Field(name string) any {
	if n.Fields == nil {
		return nil
	}
	return n.Fields[name]
}

// FullFields 管理视图: 节点 fields + ref 字段值（id 列表）— 编辑表单回显用。
func (s *Service) FullFields(id int64) (map[string]any, error) {
	n, err := s.GetNodeById(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, ErrNotFound
	}
	out := map[string]any{}
	maps.Copy(out, n.Fields)
	td, ok := s.types.Type(n.Type)
	if !ok {
		return nil, fmt.Errorf("core: type %q not defined", n.Type)
	}
	for _, f := range td.Fields {
		if !s.types.IsRefKind(f.Kind) {
			continue
		}
		ids, err := s.db.Add(`SELECT to_node FROM edges WHERE from_node = #{1} AND field = #{2} ORDER BY sort, id`, id, f.Name).FetchList[int64]()
		if err != nil {
			return nil, err
		}
		if k, ok := s.types.Kind(f.Kind); ok && k.Class() == types.ClassRef {
			if len(ids) > 0 {
				out[f.Name] = ids[0]
			}
		} else {
			anyIDs := make([]any, 0, len(ids))
			for _, tid := range ids {
				anyIDs = append(anyIDs, tid)
			}
			out[f.Name] = anyIDs
		}
	}
	return out, nil
}
