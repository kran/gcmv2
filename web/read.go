package web

import (
	"fmt"
	"strings"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// 读入口（身份绑定层）。
//
// Web 层的读有两个层次，别混：
//
//	CmsCtx.ReadPage / ReadOne / ReadAddress / ReadFull / SearchPage
//	    解析读规则（行范围 + 字段掩码）→ 调引擎 → 套字段掩码。数据跨出进程前
//	    最后一步由这里保证：调用方不需要自己拼 Scope，也不需要自己调 MaskNode。
//
//	engine.Query / GetNodeById / FullNode / ...
//	    内核原语：没有身份、没有策略。后台、插件、迁移以及"系统自己要看"的代码
//	    走这里 —— 那是显式的可信调用，不是这里的替代品。
//
// 规则解析复用 CmsCtx.ReadRule（同一请求每 (action, type) 一次）。行范围由服务端
// 计算：调用方传入非零 Scope 直接报错，避免"自己拼一个更宽的范围"。
//
// 不可见的节点返回 (nil, nil)：与 API 的 404 语义一致，怎么回由调用方决定。

// ReadPage 按读规则取一页（q.Type 必填；q.Scope 必须留空）。
// 展开（q.Expand）由引擎完成，展开出来的节点按各自类型的读规则裁字段。
func (c *CmsCtx) ReadPage(action ReadAction, q core.ListQuery) ([]core.Node, int64, error) {
	scope, err := c.resolve(action, q.Type, q.Scope)
	if err != nil {
		return nil, 0, err
	}
	q.Scope = scope
	nodes, total, err := c.site.engine.QueryPage(c.R.Context(), q)
	if err != nil {
		return nil, 0, err
	}
	if err := MaskNodes(c, action, nodes); err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}

// ReadOne 按 id 取单节点（不可见 → nil）。
func (c *CmsCtx) ReadOne(action ReadAction, id int64) (*core.Node, error) {
	node, err := c.site.engine.GetNodeById(c.R.Context(), id)
	if err != nil || node == nil {
		return nil, err
	}
	ok, err := c.visible(action, node)
	if err != nil || !ok {
		return nil, err
	}
	return MaskNode(c, action, node)
}

// ReadAddress 按 addressable capability 取单节点（不可见 → nil）。
func (c *CmsCtx) ReadAddress(action ReadAction, address string) (*core.Node, error) {
	node, err := c.site.engine.GetNodeByAddress(c.R.Context(), address)
	if err != nil || node == nil {
		return nil, err
	}
	ok, err := c.visible(action, node)
	if err != nil || !ok {
		return nil, err
	}
	return MaskNode(c, action, node)
}

// ReadFull 取单节点并带上 ref 值（编辑表单用：客户端需要知道当前选中了哪些引用）。
// 可见性与字段掩码与 ReadOne 相同（先合并 ref 值，再裁字段）。
func (c *CmsCtx) ReadFull(action ReadAction, id int64) (*core.Node, error) {
	full, err := c.site.engine.FullNode(c.R.Context(), id)
	if err != nil {
		if err == core.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	ok, err := c.visible(action, &full.Node)
	if err != nil || !ok {
		return nil, err
	}
	node := full.Node
	node.Fields = full.Values
	return MaskNode(c, action, &node)
}

// ReadTree 按读规则加载树，返回已裁字段的嵌套结构（客户端渲染树用）。
// 只要树对象（例如算子树 id 做过滤）的调用方直接用 CmsCtx.ReadRule + LoadTree。
func (c *CmsCtx) ReadTree(action ReadAction, typeName string) ([]*core.TreeNode, error) {
	scope, err := c.resolve(action, typeName, core.QueryScope{})
	if err != nil {
		return nil, err
	}
	tree, err := c.site.engine.LoadTree(c.R.Context(), typeName, scope)
	if err != nil {
		return nil, err
	}
	return MaskTree(c, action, tree.JsonNodes())
}

// SearchPage 全文检索。typeName 为空 = 所有 searchable 类型；每个类型各自解析
// ReadSearch 读规则（范围 + 掩码），结果按节点自己的类型裁字段。
func (c *CmsCtx) SearchPage(q core.SearchQuery, typeName string) ([]core.Node, int64, error) {
	targets, err := c.searchTargets(typeName)
	if err != nil {
		return nil, 0, err
	}
	q.Targets = targets
	nodes, total, err := c.site.engine.Search(c.R.Context(), q)
	if err != nil {
		return nil, 0, err
	}
	if err := MaskNodes(c, ReadSearch, nodes); err != nil {
		return nil, 0, err
	}
	return nodes, total, nil
}

// resolve 校验调用方没有自带 Scope，再按读规则解析行范围。
// 只要范围不要数据的调用方直接用 CmsCtx.ReadRule（它不接收 Scope）。
func (c *CmsCtx) resolve(action ReadAction, typeName string, given core.QueryScope) (core.QueryScope, error) {
	if typeName == "" {
		return core.QueryScope{}, fmt.Errorf("web: read type is required")
	}
	if !given.IsZero() {
		return core.QueryScope{}, fmt.Errorf(
			"web: read scope comes from the read rule; the caller must not supply one")
	}
	scope, _, err := c.ReadRule(action, typeName)
	return scope, err
}

// visible 用读规则的行范围确认单节点可见（不做字段裁剪）。
func (c *CmsCtx) visible(action ReadAction, node *core.Node) (bool, error) {
	scope, err := c.resolve(action, node.Type, core.QueryScope{})
	if err != nil {
		return false, err
	}
	rows, err := c.site.engine.Query(c.R.Context(), core.ListQuery{
		Type: node.Type, Where: gquery.EQ(gquery.System("id"), node.ID),
		Scope: scope, Page: gquery.Page{Size: 1},
	})
	if err != nil {
		return false, err
	}
	return len(rows) > 0, nil
}

// searchTargets 每个可搜索类型各自解析 ReadSearch 读规则。
func (c *CmsCtx) searchTargets(typeName string) ([]core.SearchTarget, error) {
	names := c.site.engine.Types().Names()
	if strings.TrimSpace(typeName) != "" {
		if _, ok := c.site.engine.Types().Type(typeName); !ok {
			return nil, fmt.Errorf("web: policy type %q not defined", typeName)
		}
		names = []string{typeName}
	}
	targets := make([]core.SearchTarget, 0, len(names))
	for _, name := range names {
		if _, ok := c.site.engine.Types().Searchable(name); !ok {
			continue
		}
		scope, _, err := c.ReadRule(ReadSearch, name)
		if err != nil {
			return nil, err
		}
		targets = append(targets, core.SearchTarget{Type: name, Scope: scope})
	}
	return targets, nil
}
