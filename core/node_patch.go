package core

// NodePatch 差量更新。
//
// Revision 是客户端读取到的版本；有效更新必须匹配，成功后数据库版本递增。
// Display nil 表示未提供。Fields 使用 RFC 7396 merge 语义，值为 null 表示删除。
type NodePatch struct {
	Revision *int64         `json:"revision"`
	Display  *string        `json:"display"`
	Fields   map[string]any `json:"fields,omitempty"`
}

// PatchFromNode 把已读取 Node 转为带乐观锁版本的全量 Patch。
func PatchFromNode(n *Node) *NodePatch {
	if n == nil {
		return nil
	}
	return &NodePatch{
		Revision: &n.Revision,
		Display:  &n.Display,
		Fields:   n.Fields,
	}
}
