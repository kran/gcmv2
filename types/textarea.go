package types

import "fmt"

import "strings"

// textareaKind 多行文本（kind 名 "textarea"）。
type textareaKind struct{}

// KindTextarea kind 名。名字就是后台组件文件名：web/admin/widgets/textarea.vue。
const KindTextarea = "textarea"

func (textareaKind) Name() string { return KindTextarea }
func (textareaKind) Validate(_ FieldDef, v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (textareaKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (textareaKind) Class() Class { return ClassField }
func (textareaKind) QueryOps() QueryOps {
	return QueryOps{Equal: true, Text: true}
}

func (textareaKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
