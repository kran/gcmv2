package types

// refKind 单引用: 值是一个整数节点 id。
type refKind struct{}

// KindRef kind 名; WidgetEntityPicker 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindRef = "ref"

func (refKind) Name() string { return KindRef }
func (refKind) Validate(v any) error {
	if _, err := ToID(v); err != nil {
		return err
	}
	return nil
}
func (refKind) IsEmpty(v any) bool { _, err := ToID(v); return err != nil }

func (refKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return validateRefField(t, typeName, f, defs)
}
func (refKind) Class() Class { return ClassRef }
