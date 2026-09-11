package web

import (
	"fmt"

	"github.com/kran/gcmv2/core"
)

// 写入口（身份绑定层）。
//
// 与读入口同样的两层分工：
//
//	CmsCtx.CreateNode / UpdateNode / DeleteNode
//	    "客户端发起的写"：触发 web.write.<action>.<type> 规则（身份判断 + 允许字段
//	    + 就地加工）→ 调引擎 → 返回裁剪过的节点。
//
//	engine.CreateNode / PatchNode / DeleteNode
//	    "系统自己的写"：审批、计数、导入、迁移、后台。没有客户端授权可言，不需要规则。
//	    多节点事务里的客户端写也应该自己 Fire 规则（事件总线是公开的）再用引擎落库，
//	    而不是把策略在 handler 里手写一遍。
//
// 字段白名单（规则里的 allow.Append）约束的是"客户端提交了哪些字段"，那个集合只有
// 框架自己解码时才存在（见 apiCreateNode）。站点用自建 DTO 时 DTO 就是白名单，这三个
// 入口负责的是身份与加工；规则返回空 allow 仍然视为"没有授权这个动作"（403）。

// CreateNode 走创建规则写入，返回裁剪过的节点。
func (c *CmsCtx) CreateNode(node *core.Node) (*core.Node, error) {
	if node == nil || node.Type == "" {
		return nil, fmt.Errorf("web: create needs a node with a type")
	}
	event, err := c.writeEvent(WriteCreate, node.Type)
	if err != nil {
		return nil, err
	}
	allowed := core.NewList[string]()
	if err := c.site.engine.Hooks().Fire(event, c, node, allowed); err != nil {
		return nil, err
	}
	if err := assertAllowed(WriteCreate, node.Type, allowed); err != nil {
		return nil, err
	}
	id, err := c.site.engine.CreateNode(c.R.Context(), node)
	if err != nil {
		return nil, err
	}
	return c.readBack(id)
}

// UpdateNode 走更新规则写入（patch 携带 revision 时是乐观锁更新），返回裁剪过的节点。
func (c *CmsCtx) UpdateNode(id int64, patch *core.NodePatch) (*core.Node, error) {
	if patch == nil {
		return nil, fmt.Errorf("web: update needs a patch")
	}
	existing, err := c.site.engine.GetNodeById(c.R.Context(), id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, NotFound("not found")
	}
	event, err := c.writeEvent(WriteUpdate, existing.Type)
	if err != nil {
		return nil, err
	}
	allowed := core.NewList[string]()
	if err := c.site.engine.Hooks().Fire(event, c, id, patch, allowed); err != nil {
		return nil, err
	}
	if err := assertAllowed(WriteUpdate, existing.Type, allowed); err != nil {
		return nil, err
	}
	if err := c.site.engine.PatchNode(c.R.Context(), id, patch); err != nil {
		return nil, err
	}
	return c.readBack(id)
}

// DeleteNode 走删除规则（公共删除 = 归档）。
func (c *CmsCtx) DeleteNode(id int64) error {
	existing, err := c.site.engine.GetNodeById(c.R.Context(), id)
	if err != nil {
		return err
	}
	if existing == nil {
		return NotFound("not found")
	}
	event, err := c.writeEvent(WriteDelete, existing.Type)
	if err != nil {
		return err
	}
	if err := c.site.engine.Hooks().Fire(event, c, id); err != nil {
		return err
	}
	return c.site.engine.DeleteNode(c.R.Context(), id)
}

// writeEvent 取写事件名；未注册规则的类型直接拒绝（匿名 401 / 已认证 403）。
func (c *CmsCtx) writeEvent(action WriteAction, typeName string) (string, error) {
	event := WriteEvent(action, typeName)
	if !c.site.engine.Hooks().Has(event) {
		return "", deniedWrite(c, action, typeName)
	}
	return event, nil
}

// assertAllowed 规则一个字段都没放行 = 没有授权这个动作（与公开写端点同语义）。
func assertAllowed(action WriteAction, typeName string, allowed *core.List[string]) error {
	if allowed.Len() == 0 {
		return Forbidden("%s is not allowed for type %q", action, typeName)
	}
	return nil
}

// readBack 写响应里的节点按 ReadView 规则裁字段 —— "注册规则 ⇒ 输出已裁"对写响应
// 同样成立。
func (c *CmsCtx) readBack(id int64) (*core.Node, error) {
	node, err := c.site.engine.GetNodeById(c.R.Context(), id)
	if err != nil || node == nil {
		return nil, err
	}
	return MaskNode(c, ReadView, node)
}
