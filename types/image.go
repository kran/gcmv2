package types

import "fmt"

import "strings"

// imageKind 图片 URL/路径。
type imageKind struct{}

// KindImage kind 名; WidgetUploadImage 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindImage = "upload-image"

func (imageKind) Name() string { return KindImage }
func (imageKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (imageKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (imageKind) Class() Class { return ClassField }

func (imageKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
