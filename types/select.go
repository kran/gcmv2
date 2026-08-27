package types

import (
	"fmt"
	"slices"
	"strings"
)

// selectKind 下拉选择（选项来自字段声明的 options; 值 = 选中的字符串）。
type selectKind struct{}

// KindSelect kind 名。
const KindSelect = "select"

func (selectKind) Name() string { return KindSelect }
func (selectKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil // 选项校验在 ValidateField（需要字段定义）
}
func (selectKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (selectKind) Class() Class { return ClassField }

func (selectKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	if err := rejectRefAttrs(typeName, f); err != nil {
		return err
	}
	if len(f.Options) == 0 {
		return fmt.Errorf("types: type %q field %q: select requires options", typeName, f.Name)
	}
	// 去重校验
	seen := map[string]bool{}
	for _, o := range f.Options {
		if seen[o] {
			return fmt.Errorf("types: type %q field %q: duplicate option %q", typeName, f.Name, o)
		}
		seen[o] = true
	}
	return nil
}

// optionValid select 值必须在字段声明的 options 内（写入校验）。
func (k selectKind) optionValid(f FieldDef, v string) bool {
	return slices.Contains(f.Options, v)
}
