package types

import "fmt"

// refListKind 多引用: 值是节点 id 数组。
type refListKind struct{}

// KindRefList kind 名; WidgetEntityList 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindRefList = "ref[]"

func (refListKind) Name() string { return KindRefList }
func (refListKind) Validate(v any) error {
	arr, ok := v.([]any)
	if !ok {
		return fmt.Errorf("expects array of node ids, got %T", v)
	}
	for i, e := range arr {
		if _, err := ToID(e); err != nil {
			return fmt.Errorf("[%d]: %w", i, err)
		}
	}
	return nil
}
func (refListKind) IsEmpty(v any) bool {
	arr, ok := v.([]any)
	return !ok || len(arr) == 0
}
func (refListKind) Class() Class { return ClassRefList }

func (refListKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return validateRefField(t, typeName, f, defs)
}
