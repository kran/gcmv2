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

// Validate 值必须是字段 options 内的字符串。选项属于字段定义, 因此这里需要
// FieldDef — 容器不再按 Kind 名特判。
func (selectKind) Validate(f FieldDef, v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	if !slices.Contains(f.Options, s) {
		return fmt.Errorf("%q not in options %v", s, f.Options)
	}
	return nil
}
func (selectKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (selectKind) Class() Class { return ClassField }
func (selectKind) QueryOps() QueryOps {
	return QueryOps{Equal: true, Sortable: true}
}

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
