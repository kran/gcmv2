package types

import (
	"fmt"
	"slices"
)

const (
	AddressUniqueGlobal = "global"
	AdminViewList       = "list"
	AdminViewTree       = "tree"
)

// ApplyDefaults 返回字段副本，并只为创建时缺失的字段应用默认值。
func (t *Types) ApplyDefaults(typeName string, fields map[string]any) (map[string]any, error) {
	td, ok := t.defs[typeName]
	if !ok {
		return nil, fmt.Errorf("types: type %q not defined", typeName)
	}
	out := make(map[string]any, len(fields)+len(td.Fields))
	for name, value := range fields {
		out[name] = value
	}
	for _, field := range td.Fields {
		if _, exists := out[field.Name]; !exists && field.Default != nil {
			out[field.Name] = cloneValue(field.Default)
		}
	}
	return out, nil
}

// IsPublished 判断节点字段是否满足该类型显式 publication 能力。
// 未声明 publication 的类型不是公开内容。
func (t *Types) IsPublished(typeName string, fields map[string]any) bool {
	td, ok := t.defs[typeName]
	if !ok || td.Capabilities.Publication == nil {
		return false
	}
	capability := td.Capabilities.Publication
	value, ok := fields[capability.Field].(string)
	return ok && value == capability.Published
}

// Address 返回该类型的公开地址字段值；未声明或值非法时返回空串。
func (t *Types) Address(typeName string, fields map[string]any) string {
	td, ok := t.defs[typeName]
	if !ok || td.Capabilities.Addressable == nil {
		return ""
	}
	value, _ := fields[td.Capabilities.Addressable.Field].(string)
	return value
}

// Searchable 返回 searchable 配置。
func (t *Types) Searchable(typeName string) (SearchableCapability, bool) {
	td, ok := t.defs[typeName]
	if !ok || td.Capabilities.Searchable == nil {
		return SearchableCapability{}, false
	}
	return *td.Capabilities.Searchable, true
}

// Addressable 返回 addressable 配置。
func (t *Types) Addressable(typeName string) (AddressableCapability, bool) {
	td, ok := t.defs[typeName]
	if !ok || td.Capabilities.Addressable == nil {
		return AddressableCapability{}, false
	}
	return *td.Capabilities.Addressable, true
}

// Publication 返回 publication 配置。
func (t *Types) Publication(typeName string) (PublicationCapability, bool) {
	td, ok := t.defs[typeName]
	if !ok || td.Capabilities.Publication == nil {
		return PublicationCapability{}, false
	}
	return *td.Capabilities.Publication, true
}

// Tree 返回 tree 配置。
func (t *Types) Tree(typeName string) (TreeCapability, bool) {
	td, ok := t.defs[typeName]
	if !ok || td.Capabilities.Tree == nil {
		return TreeCapability{}, false
	}
	return *td.Capabilities.Tree, true
}

func (t *Types) validateTypeConfig(typeName string, td TypeDef) error {
	field := func(name string) (FieldDef, error) {
		f, ok := FieldByName(td, name)
		if !ok {
			return FieldDef{}, fmt.Errorf("types: type %q: field %q is not defined", typeName, name)
		}
		return f, nil
	}

	if searchable := td.Capabilities.Searchable; searchable != nil {
		if len(searchable.Fields) == 0 {
			return fmt.Errorf("types: type %q: searchable.fields required", typeName)
		}
		for _, name := range searchable.Fields {
			if name == "display" {
				continue
			}
			f, err := field(name)
			if err != nil {
				return err
			}
			if !slices.Contains([]string{KindString, KindText, KindRichtext, KindSlug}, f.Kind) {
				return fmt.Errorf("types: type %q: searchable field %q must be textual", typeName, name)
			}
		}
	}

	if addressable := td.Capabilities.Addressable; addressable != nil {
		f, err := field(addressable.Field)
		if err != nil {
			return err
		}
		if f.Kind != KindSlug {
			return fmt.Errorf("types: type %q: addressable field %q must use kind %q", typeName, f.Name, KindSlug)
		}
		if addressable.Unique != AddressUniqueGlobal {
			return fmt.Errorf("types: type %q: addressable.unique must be %q", typeName, AddressUniqueGlobal)
		}
	}

	if publication := td.Capabilities.Publication; publication != nil {
		f, err := field(publication.Field)
		if err != nil {
			return err
		}
		if f.Kind != KindSelect {
			return fmt.Errorf("types: type %q: publication field %q must use kind select", typeName, f.Name)
		}
		if publication.Draft == "" || publication.Published == "" || publication.Draft == publication.Published {
			return fmt.Errorf("types: type %q: publication draft/published must be non-empty and different", typeName)
		}
		if !slices.Contains(f.Options, publication.Draft) || !slices.Contains(f.Options, publication.Published) {
			return fmt.Errorf("types: type %q: publication values must be options of field %q", typeName, f.Name)
		}
	}

	if tree := td.Capabilities.Tree; tree != nil {
		parent, err := field(tree.Parent)
		if err != nil {
			return err
		}
		if parent.Kind != KindRef || parent.To != typeName {
			return fmt.Errorf("types: type %q: tree.parent %q must be a self ref", typeName, tree.Parent)
		}
		if tree.Order != "" {
			order, err := field(tree.Order)
			if err != nil {
				return err
			}
			if order.Kind != KindNumber {
				return fmt.Errorf("types: type %q: tree.order %q must use kind number", typeName, tree.Order)
			}
		}
	}

	view := td.Admin.View
	if view != "" && view != AdminViewList && view != AdminViewTree {
		return fmt.Errorf("types: type %q: admin.view must be list or tree", typeName)
	}
	if view == AdminViewTree && td.Capabilities.Tree == nil {
		return fmt.Errorf("types: type %q: admin tree view requires tree capability", typeName)
	}
	for _, name := range td.Admin.Columns {
		if IsNodeColumn(name) && name != "fields" {
			continue
		}
		if _, err := field(name); err != nil {
			return err
		}
	}

	if err := t.validateConstraintGroups(typeName, td, "unique", td.Constraints.Unique); err != nil {
		return err
	}
	return t.validateConstraintGroups(typeName, td, "indexes", td.Constraints.Indexes)
}

func (t *Types) validateConstraintGroups(typeName string, td TypeDef, kind string, groups [][]string) error {
	seenGroups := map[string]bool{}
	for _, group := range groups {
		if len(group) == 0 {
			return fmt.Errorf("types: type %q: %s group must not be empty", typeName, kind)
		}
		seenFields := map[string]bool{}
		key := ""
		for _, name := range group {
			if seenFields[name] {
				return fmt.Errorf("types: type %q: %s group repeats field %q", typeName, kind, name)
			}
			seenFields[name] = true
			f, ok := FieldByName(td, name)
			if !ok {
				return fmt.Errorf("types: type %q: %s field %q is not defined", typeName, kind, name)
			}
			if t.IsRefKind(f.Kind) {
				return fmt.Errorf("types: type %q: %s field %q cannot be a ref in v0.9", typeName, kind, name)
			}
			key += "\x00" + name
		}
		if seenGroups[key] {
			return fmt.Errorf("types: type %q: duplicate %s group %v", typeName, kind, group)
		}
		seenGroups[key] = true
	}
	return nil
}

func cloneValue(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i := range v {
			out[i] = cloneValue(v[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = cloneValue(item)
		}
		return out
	default:
		return value
	}
}
