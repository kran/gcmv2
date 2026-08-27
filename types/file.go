package types

import "fmt"

import "strings"

// fileKind 文件 URL/路径（上传产物）。
type fileKind struct{}

// KindFile kind 名; WidgetUploadFile 编辑控件原语（各 kind 自包含定义 — 加新 kind
// 只动一个文件）。
const KindFile = "upload-file"

func (fileKind) Name() string { return KindFile }
func (fileKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (fileKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (fileKind) Class() Class { return ClassField }

func (fileKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
