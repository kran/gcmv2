package core

import (
	"encoding/json"

	"github.com/kran/dba"
)

// NodePatch 差量更新（PATCH 语义 — 仅 PatchNode 用）。
//
// 指针字段: nil = 未提供（不更新）; 非 nil = 显式设置（含零值 — 如 SetSlug("") 清空）。
// Fields: merge 语义 — 提供的 key 覆盖/新增, 未提供的保留（json_patch 下沉 SQLite）;
//
//	key 值为 null = 删除该字段（json_patch RFC 7396）。
//
// 构造: 请求 JSON 直接 Unmarshal（null/缺失 → nil）; 表单全量用 PatchFromNode。
type NodePatch struct {
	Slug   *string        `json:"slug"`
	Status *int           `json:"status"`
	Sort   *int           `json:"sort"`
	Title  *string        `json:"title"`
	Fields map[string]any `json:"fields,omitempty"`
}

// PatchFromNode Node → 全非 nil patch（表单"读-改-写"全量提交用）:
// 读出节点 → 改 patch 字段 → PatchNode。全非 nil = 全量写（值同无害）。
func PatchFromNode(n *Node) *NodePatch {
	return &NodePatch{
		Slug:   &n.Slug,
		Status: &n.Status,
		Sort:   &n.Sort,
		Title:  &n.Title,
		Fields: n.Fields,
	}
}

// Cols 返回要更新的列 map（非 nil 列 → entry）— dba.Update map 驱动。
// fields 有变化 → dba.Expr 内联 json_patch（merge 语义 — 一个 UPDATE 搞定）。
// 空 map = 无列更新。
func (p *NodePatch) Cols() map[string]any {
	cols := map[string]any{}
	if p.Slug != nil {
		cols["slug"] = *p.Slug
	}
	if p.Status != nil {
		cols["status"] = *p.Status
	}
	if p.Sort != nil {
		cols["sort"] = *p.Sort
	}
	if p.Title != nil {
		cols["title"] = *p.Title
	}
	if len(p.Fields) > 0 {
		b, err := json.Marshal(p.Fields)
		if err == nil {
			cols["fields"] = dba.Expr(`json_patch(fields, #{1})`, string(b))
		}
	}
	return cols
}
