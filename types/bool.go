package types

import "fmt"

// boolKind 布尔。
type boolKind struct{}

// KindBool kind 名; WidgetSwitch 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindBool = "bool"

func (boolKind) Name() string { return KindBool }
func (boolKind) Validate(v any) error {
	if _, ok := v.(bool); !ok {
		return fmt.Errorf("expects bool, got %T", v)
	}
	return nil
}
func (boolKind) IsEmpty(v any) bool { _, ok := v.(bool); return !ok }

func (boolKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
func (boolKind) Class() Class { return ClassField }
