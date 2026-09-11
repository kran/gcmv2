package web

import (
	"maps"

	"github.com/kran/gcmv2/core"
)

// 字段级可见性（读规则的第二个出参）。
//
// 读规则 web.read.<action>.<type> 除了收窄行范围（*gquery.Expr），还可以声明
// “这个角色看不到哪些字段”（hide）。解析集中在 CmsCtx.ReadRule —— 同一请求内
// 按 (action, type) 只触发一次规则；应用在本文件，只做三件事：拷贝、删键、
// 按子节点自己的类型递归处理 Expand。
//
// 约定:
//   - hide 是顶层字段名；命中即从 JSON 里消失（不是置空）。
//   - hide 为空 = 全部字段可见 —— 未注册规则、规则不碰 hide 都是这个语义。
//   - 只影响输出，不影响查询能力（排序/筛选/检索照旧，行集大小不变）。
//   - 后台与插件（BypassPolicy 路径）、直接调 core 的代码不做字段裁剪。
//   - Expand 是引擎构造的有限树（多跳路径展开），裁剪只处理 *Node / []*Node。
//   - 只读：输入节点永不被就地修改；需要裁剪时返回拷贝，无需裁剪时原样返回。

// MaskNode 返回裁剪后的节点。hide 为空时原样返回（不拷贝，零分配）。
//
// 调用方拿返回值继续用即可：既不要假设原节点被改，也不要依赖返回值 != 入参。
func MaskNode(c *CmsCtx, action ReadAction, node *core.Node) (*core.Node, error) {
	if node == nil {
		return nil, nil
	}
	_, hide, err := c.ReadRule(action, node.Type)
	if err != nil {
		return nil, err
	}
	expand, expandChanged, err := maskExpand(c, action, node.Expand)
	if err != nil {
		return nil, err
	}
	if len(hide) == 0 && !expandChanged {
		return node, nil
	}
	out := *node
	if len(hide) > 0 {
		out.Fields = maps.Clone(node.Fields)
		for _, name := range hide {
			delete(out.Fields, name)
		}
	}
	if expandChanged {
		out.Expand = expand
	}
	return &out, nil
}

// MaskNodes 就地裁剪切片元素（每个节点按自己的类型解析规则）。
// 站点 handler 输出 []core.Node 时调用 —— 框架内部路径（API/模板）已自动套用。
func MaskNodes(c *CmsCtx, action ReadAction, nodes []core.Node) error {
	for i := range nodes {
		masked, err := MaskNode(c, action, &nodes[i])
		if err != nil {
			return err
		}
		nodes[i] = *masked
	}
	return nil
}

// MaskTree 裁剪树结构（TreeNode 内联了完整节点 + children）。
func MaskTree(c *CmsCtx, action ReadAction, nodes []*core.TreeNode) ([]*core.TreeNode, error) {
	out, _, err := maskTreeNodes(c, action, nodes)
	return out, err
}

// maskNodesPtr 裁剪指针切片（只在真的裁剪了元素时才拷切片）。
func maskNodesPtr(c *CmsCtx, action ReadAction, nodes []*core.Node) ([]*core.Node, error) {
	out, copied := nodes, false
	for i, node := range nodes {
		masked, err := MaskNode(c, action, node)
		if err != nil {
			return nil, err
		}
		if masked == node {
			continue
		}
		if !copied {
			out = append([]*core.Node(nil), nodes...)
			copied = true
		}
		out[i] = masked
	}
	return out, nil
}

// maskExpand 裁剪展开容器。只有真的改了子节点才返回新 map（changed = true）。
func maskExpand(c *CmsCtx, action ReadAction, in map[string]any) (map[string]any, bool, error) {
	if len(in) == 0 {
		return in, false, nil
	}
	out, changed := in, false
	for key, value := range in {
		switch typed := value.(type) {
		case *core.Node:
			masked, err := MaskNode(c, action, typed)
			if err != nil {
				return nil, false, err
			}
			if masked == typed {
				continue
			}
			if !changed {
				out = maps.Clone(in)
				changed = true
			}
			out[key] = masked
		case []*core.Node:
			list, listCopied := typed, false
			for i, child := range typed {
				masked, err := MaskNode(c, action, child)
				if err != nil {
					return nil, false, err
				}
				if masked == child {
					continue
				}
				if !listCopied {
					list = append([]*core.Node(nil), typed...)
					listCopied = true
				}
				list[i] = masked
			}
			if !listCopied {
				continue
			}
			if !changed {
				out = maps.Clone(in)
				changed = true
			}
			out[key] = list
		}
	}
	return out, changed, nil
}

// maskTreeNodes 裁剪树节点。changed 表示这一层（或更深）有节点被裁剪。
func maskTreeNodes(c *CmsCtx, action ReadAction, nodes []*core.TreeNode) ([]*core.TreeNode, bool, error) {
	if len(nodes) == 0 {
		return nodes, false, nil
	}
	out, changed := nodes, false
	for i, item := range nodes {
		if item == nil {
			continue
		}
		// MaskNode 不改入参: 没裁剪时返回同一个指针。
		node, err := MaskNode(c, action, &item.Node)
		if err != nil {
			return nil, false, err
		}
		children, childChanged, err := maskTreeNodes(c, action, item.Children)
		if err != nil {
			return nil, false, err
		}
		if node == &item.Node && !childChanged {
			continue
		}
		if !changed {
			out = append([]*core.TreeNode(nil), nodes...)
			changed = true
		}
		out[i] = &core.TreeNode{Node: *node, Children: children}
	}
	return out, changed, nil
}
