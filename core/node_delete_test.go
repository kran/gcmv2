package core

import (
	"errors"
	"testing"
)

// 删除语义只有一种：被引用就不许删（restrict）。错误里要能看出"谁在引用"。
const deleteTypesYAML = `
types:
  account:
    fields:
      - { name: name, kind: text }
  note:
    fields:
      - { name: text, kind: text }
      - { name: account, kind: ref, to: account }
  memo:
    fields:
      - { name: text, kind: text }
      - { name: accounts, kind: "refs", to: account }
`

func TestDeleteIsRestrictedByIncomingReferences(t *testing.T) {
	s := New(testDB(t), newTypes(t, deleteTypesYAML))
	account, _ := s.CreateNode(t.Context(), &Node{Type: "account", Display: "甲", Fields: Fields{"name": "甲"}})
	other, _ := s.CreateNode(t.Context(), &Node{Type: "account", Display: "乙", Fields: Fields{"name": "乙"}})
	single, _ := s.CreateNode(t.Context(), &Node{Type: "note", Display: "引用了甲", Fields: Fields{"account": account}})
	multi, _ := s.CreateNode(t.Context(), &Node{Type: "memo", Display: "也引用了甲", Fields: Fields{"accounts": []any{account}}})

	// 有入边 → 拒绝，且错误里带出引用方（界面据此提示先解除哪条引用）
	err := s.DeleteNode(t.Context(), account)
	if !errors.Is(err, ErrDeleteRestricted) {
		t.Fatalf("被引用仍可删: %v", err)
	}
	var restricted *DeleteRestrictedError
	if !errors.As(err, &restricted) {
		t.Fatalf("错误类型 = %T", err)
	}
	if restricted.NodeID != account || len(restricted.References) != 2 {
		t.Fatalf("引用信息 = %#v", restricted)
	}
	for _, ref := range restricted.References {
		if ref.SourceID != single && ref.SourceID != multi {
			t.Fatalf("引用方 = %#v", ref)
		}
	}

	// 无入边的引用方自己可以被删（它的出边不是限制，删时顺带清掉）
	if err := s.DeleteNode(t.Context(), multi); err != nil {
		t.Fatalf("无入边的节点应可删: %v", err)
	}
	// 换掉最后一个引用后，目标才可以删
	current, err := s.GetNode(t.Context(), single)
	if err != nil {
		t.Fatal(err)
	}
	revision := current.Revision
	if err := s.PatchNode(t.Context(), single, &NodePatch{Revision: &revision, Fields: Fields{"account": other}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(t.Context(), account); err != nil {
		t.Fatalf("解除全部引用后应可删: %v", err)
	}
	if singleNode, err := s.GetNode(t.Context(), single); err != nil || singleNode.Fields["account"] != other {
		t.Fatalf("引用方应保留并指向新目标: %#v %v", singleNode, err)
	}
}
