package types

import "fmt"

// numberKind 数值（int/uint 家族 + float64）。
type numberKind struct{}

// KindNumber kind 名; WidgetNumber 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindNumber = "number"

func (numberKind) Name() string { return KindNumber }
func (numberKind) Validate(v any) error {
	if !isNumber(v) {
		return fmt.Errorf("expects number, got %T", v)
	}
	return nil
}
func (numberKind) IsEmpty(v any) bool { return !isNumber(v) }

func (numberKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
func (numberKind) Class() Class { return ClassField }
