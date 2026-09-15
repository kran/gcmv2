package types

import (
	"fmt"
	"strings"
)

// textKind 单行文本（kind 名 "text"）。
type textKind struct{}

// KindText kind 名。名字就是后台组件文件名：web/admin/widgets/text.vue。
const KindText = "text"

func (textKind) Name() string { return KindText }
func (textKind) Validate(_ FieldDef, v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (textKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (textKind) Class() Class { return ClassField }
func (textKind) QueryOps() QueryOps {
	return QueryOps{Equal: true, Text: true, Sortable: true}
}

func (textKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
