package core

// DB 读写（dba.SQL 手写 — Node 是值模型, 不用 Dao 泛型）。
// 列白名单防注入; fields 一律 json_patch merge。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kran/dba"
	"github.com/kran/gcmv2/types"
	_ "modernc.org/sqlite" // sqlite driver 注册
)

var (
	// ErrNotFound 目标节点不存在。
	ErrNotFound = errors.New("core: node not found")
	// ErrRevisionConflict 节点在客户端读取后已被其他写入修改。
	ErrRevisionConflict = errors.New("core: node revision conflict")
	// ErrInvalidFields means the submitted fields failed Schema validation
	// (missing required, wrong Kind, immutable, duplicate ref target).
	ErrInvalidFields = errors.New("core: invalid fields")
)

// invalidFields 包装 Schema/值校验错误（Web 边界据此返回 422）。
// nodePtrs 值切片 → 指针切片（读面统一返回 *Node）。
func nodePtrs(nodes []Node) []*Node {
	out := make([]*Node, len(nodes))
	for i := range nodes {
		out[i] = &nodes[i]
	}
	return out
}

// GetNode 单个读：Fields 一律完整（引用 id 已在其中）。不存在返回 ErrNotFound。
//
// ref 直接给"节点是什么"：int / int64 = id，非空 string = 地址（addressable 的全局地址）。
// 别的类型 fail-loud —— 定位方式就这两种，不做包装类型。
func (s *Service) GetNode(ctx context.Context, ref any) (*Node, error) {
	var row *Node
	var err error
	switch v := ref.(type) {
	case int64:
		row, err = s.nodeRow(ctx, v)
	case int:
		row, err = s.nodeRow(ctx, int64(v))
	case string:
		if v == "" {
			return nil, fmt.Errorf("%w: empty ref", ErrInvalidQuery)
		}
		// 字符串先当 id（纯数字），不是 id 或者查不到再当地址 —— 调用方不必自己判断
		// （路由里的 /node/{id_or_address} 就是这样）。
		if refID, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			row, err = s.nodeRow(ctx, refID)
		}
		if err == nil && row == nil {
			row, err = s.nodeRowByAddress(ctx, v)
		}
	default:
		return nil, fmt.Errorf("%w: GetNode needs an id (int64) or an address (string), got %T",
			ErrInvalidQuery, ref)
	}
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrNotFound
	}
	nodes := []Node{*row}
	if err := s.hydrateFields(ctx, nodes); err != nil {
		return nil, err
	}
	return &nodes[0], nil
}

func invalidFields(err error) error {
	return fmt.Errorf("%w: %w", ErrInvalidFields, err)
}

// ── 读 ────────────────────────────────────────

// nodeRow 按 id 取"存储行"（只有标量 Fields, 没有引用 id）—— 写入路径与索引重建用。
// 公开读走 GetNode（Fields 完整）。不存在返回 (nil, nil)。
func (s *Service) nodeRow(ctx context.Context, id int64) (*Node, error) {
	n, err := s.db.WithCtx(ctx).Select("nodes", `id = #{1}`, id).FetchOne[Node]()
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, nil
	}
	return n, nil
}

// nodeRowByAddress 按 addressable capability 的全局地址查"存储行"（同上, 内部用）。
func (s *Service) nodeRowByAddress(ctx context.Context, address string) (*Node, error) {
	if address == "" {
		return nil, nil
	}
	conditions := make([]string, 0)
	args := make([]any, 0)
	for _, typeName := range s.types.Names() {
		capability, ok := s.types.Addressable(typeName)
		if !ok {
			continue
		}
		// JSON 路径写成字面量: 绑参数时 SQLite 无法把它与索引表达式对上 → 退化成全类型扫描
		// （GetNodeByAddress 的 type= AND json_extract(...) 两条条件正好命中上面的单类型地址索引）
		start := len(args) + 1
		conditions = append(conditions, fmt.Sprintf(
			`(type = #{%d} AND json_extract(fields, %s) = #{%d})`,
			start, quoteLiteral("$."+capability.Field), start+1))
		args = append(args, typeName, address)
	}
	if len(conditions) == 0 {
		return nil, nil
	}
	query := `SELECT * FROM nodes WHERE (` + strings.Join(conditions, " OR ") + `) LIMIT 1`
	n, err := s.db.WithCtx(ctx).Add(query, args...).FetchOne[Node]()
	if err != nil {
		return nil, err
	}
	return n, nil
}

// ── 写: Create ─────────────────────────────────

// CreateNode 建节点: 校验 → 事务（BeforeCreate → INSERT → ref 落边 → AfterCreate）。
// 返回新节点 ID; 不修改调用方传入的 Node。未知字段直接报错，避免拼写错误和
// 客户端/Schema 漂移被静默吞掉。
func (s *Service) CreateNode(ctx context.Context, n *Node) (int64, error) {
	if n == nil {
		return 0, errors.New("core: create: nil node")
	}
	td, ok := s.types.Type(n.Type)
	if !ok {
		return 0, fmt.Errorf("core: type %q not defined", n.Type)
	}
	fields, err := s.types.ApplyDefaults(n.Type, n.Fields)
	if err != nil {
		return 0, invalidFields(err)
	}
	if err := s.types.ValidateFields(n.Type, fields); err != nil {
		return 0, invalidFields(err)
	}
	if n.Display == "" {
		return 0, invalidFields(errors.New("core: create: display required"))
	}
	// 内部拷贝，不触碰调用方。BeforeCreate 可以补充字段，因此事务内会
	// 再次校验并在校验后拆分引用。
	m := *n
	m.ID = 0
	m.Fields = Fields(fields)
	m.Revision = 1
	now := TimeOf(time.Now())
	m.CreatedAt = now
	m.UpdatedAt = now

	var id int64
	err = s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		err := s.hooks.Fire(HookNodeBeforeCreate, tx, &m)
		if err != nil {
			return err
		}
		if m.Type != td.Name {
			return errors.New("core: create: type is immutable")
		}
		if m.Display == "" {
			return invalidFields(errors.New("core: create: display required"))
		}
		m.ID = 0
		m.Revision = 1
		m.CreatedAt = now
		m.UpdatedAt = now
		if err = s.types.ValidateFields(m.Type, m.Fields); err != nil {
			return invalidFields(err)
		}
		scalar, refs, err := splitRefs(td, s.types, m.Fields)
		if err != nil {
			return err
		}
		m.Fields = scalar
		res, err := tx.Insert("nodes", &m).Exec()
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		if err != nil {
			return err
		}
		m.ID = id
		if err := addEdges(tx, s.types, td, id, refs); err != nil {
			return err
		}
		return s.hooks.Fire(HookNodeAfterCreate, tx, &m)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ── 写: Patch（差量） ──────────────────────────

// PatchNode 差量更新: 非 nil 列（dba.Update map）+ fields json_patch merge。
// 空 patch（全 nil + fields 空）→ 零 UPDATE（幂等）。
func (s *Service) PatchNode(ctx context.Context, id int64, patch *NodePatch) error {
	if patch == nil {
		return errors.New("core: patch: nil patch")
	}
	existing, err := s.nodeRow(ctx, id)
	if err != nil {
		return err
	}
	if existing == nil {
		return ErrNotFound
	}
	if patch.Display == nil && len(patch.Fields) == 0 {
		return nil
	}
	if patch.Revision == nil || *patch.Revision <= 0 {
		return fmt.Errorf("%w: revision required", ErrInvalidFields)
	}
	td, ok := s.types.Type(existing.Type)
	if !ok {
		return fmt.Errorf("core: type %q not defined", existing.Type)
	}

	return s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		err := s.hooks.Fire(HookNodeBeforeUpdate, tx, patch)
		if err != nil {
			return err
		}
		if err = s.types.ValidatePatchFields(existing.Type, patch.Fields); err != nil {
			return invalidFields(err)
		}

		scalarPatch := Fields{}
		refPatch := map[string]any{}
		for name, value := range patch.Fields {
			field, ok := types.FieldByName(td, name)
			if !ok {
				return fmt.Errorf("core: field %q not on type %q", name, existing.Type)
			}
			if s.types.IsRefKind(field.Kind) {
				refPatch[name] = value
			} else {
				scalarPatch[name] = value
			}
		}

		cols := map[string]any{}
		if patch.Display != nil {
			if *patch.Display == "" {
				return invalidFields(errors.New("core: patch: display required"))
			}
			cols["display"] = *patch.Display
		}
		if len(scalarPatch) > 0 {
			body, err := json.Marshal(scalarPatch)
			if err != nil {
				return fmt.Errorf("core: patch fields: %w", err)
			}
			cols["fields"] = dba.Expr(`json_patch(fields, #{1})`, string(body))
		}
		if len(cols) == 0 && len(refPatch) == 0 {
			return nil
		}

		cols["updated_at"] = TimeOf(time.Now())
		cols["revision"] = dba.Expr(`revision + 1`)
		result, err := tx.Update("nodes", cols, `id = #{1} AND revision = #{2}`, id, *patch.Revision).Exec()
		if err != nil {
			return err
		}
		updatedRows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if updatedRows == 0 {
			return ErrRevisionConflict
		}

		for name := range refPatch {
			field, _ := types.FieldByName(td, name)
			err := deleteFieldEdges(tx, id, field)
			if err != nil {
				return err
			}
		}
		if len(refPatch) > 0 {
			err = addEdges(tx, s.types, td, id, refPatch)
			if err != nil {
				return err
			}
		}

		updated, err := tx.Select("nodes", `id = #{1}`, id).FetchOne[Node]()
		if err != nil {
			return err
		}
		if updated == nil {
			return fmt.Errorf("core: update: node %d vanished in tx", id)
		}
		return s.hooks.Fire(HookNodeAfterUpdate, tx, updated)
	})
}

// ── 写: Delete ────────────────────────────────

// DeleteNode permanently deletes a Node after applying every incoming
// incoming references in one transaction.
func (s *Service) DeleteNode(ctx context.Context, id int64) error {
	return s.db.WithCtx(ctx).Transaction(func(tx *dba.SQL) error {
		return s.deleteNodeTx(tx, id)
	})
}
