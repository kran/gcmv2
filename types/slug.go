package types

import (
	"fmt"
	"strings"
)

// KindSlug 是可寻址实体的 URL 段。是否参与公开路由由 addressable capability 决定。
const KindSlug = "slug"

type slugKind struct{}

func (slugKind) Name() string { return KindSlug }

func (slugKind) Validate(value any) error {
	slug, ok := value.(string)
	if !ok {
		return fmt.Errorf("expects slug string, got %T", value)
	}
	if slug != "" && !ValidSlug(slug) {
		return fmt.Errorf("invalid slug %q", slug)
	}
	return nil
}

func (slugKind) IsEmpty(value any) bool {
	slug, ok := value.(string)
	return !ok || strings.TrimSpace(slug) == ""
}

func (slugKind) Class() Class { return ClassField }
func (slugKind) QueryOps() QueryOps {
	return QueryOps{Equal: true, Text: true, Sortable: true}
}

func (slugKind) ValidateField(t *Types, typeName string, field FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, field)
}
