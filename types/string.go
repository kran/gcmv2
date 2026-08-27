package types

import (
	"fmt"
	"strings"
)

// stringKind 单行字符串。
type stringKind struct{}

// KindString kind 名; WidgetInput 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindString = "text"

func (stringKind) Name() string { return KindString }
func (stringKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (stringKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (stringKind) Class() Class { return ClassField }

func (stringKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
