// Package types gcm 的字段类型组件。
//
// 组件形态: 容器类型（Types, 分站一个实例）+ Kind 接口 + 内置具体实现。
// - Kind 接口在 kind.go, 每个内置 kind 一个文件（string.go/ref.go/...）。
// - Types 容器: New() 默认注册内置 kind, 站点用 RegisterKind 扩展（重复注册 panic）。
// - 类型定义（TypeDef/Load/整体校验）是本文件的组织层; 代数校验是宪法, 不对外可改。
//
// 原则: fail-loud — 未知 kind / 重复注册 / 非法定义, 加载即报错, 绝不静默。
package types

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// TypeDef 类型定义。Schema、运行能力和后台展示显式分组，避免展示配置
// 影响数据不变量。
type TypeDef struct {
	Name         string       `json:"name"` // 类型名（配置键）
	Fields       []FieldDef   `yaml:"fields" json:"fields"`
	Constraints  Constraints  `yaml:"constraints,omitempty" json:"constraints,omitempty"`
	Capabilities Capabilities `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Admin        AdminView    `yaml:"admin,omitempty" json:"admin,omitempty"`
}

// Constraints 类型级数据库约束。v0.9 首期只接受标量字段；引用参与的
// 组合约束留给关系约束实现，不能用应用层假校验冒充数据库不变量。
type Constraints struct {
	Unique  [][]string `yaml:"unique,omitempty" json:"unique,omitempty"`
	Indexes [][]string `yaml:"indexes,omitempty" json:"indexes,omitempty"`
}

// Capabilities 类型选择启用的通用行为。
type Capabilities struct {
	Searchable     *SearchableCapability  `yaml:"searchable,omitempty" json:"searchable,omitempty"`
	Addressable    *AddressableCapability `yaml:"addressable,omitempty" json:"addressable,omitempty"`
	Publication    *PublicationCapability `yaml:"publication,omitempty" json:"publication,omitempty"`
	Authentication bool                   `yaml:"authentication,omitempty" json:"authentication,omitempty"`
	Tree           *TreeCapability        `yaml:"tree,omitempty" json:"tree,omitempty"`
	Relation       *RelationCapability    `yaml:"relation,omitempty" json:"relation,omitempty"`
}

type SearchableCapability struct {
	Fields []string `yaml:"fields" json:"fields"`
}

type AddressableCapability struct {
	Field  string `yaml:"field" json:"field"`
	Unique string `yaml:"unique" json:"unique"`
}

type PublicationCapability struct {
	Field     string `yaml:"field" json:"field"`
	Draft     string `yaml:"draft" json:"draft"`
	Published string `yaml:"published" json:"published"`
}

type TreeCapability struct {
	Parent string `yaml:"parent" json:"parent"`
	Order  string `yaml:"order,omitempty" json:"order,omitempty"`
}

// RelationCapability marks a normal Node type as an attributed relation.
// From and To name its required endpoint ref fields.
type RelationCapability struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
}

// OnDelete controls permanent deletion of a referenced target.
type OnDelete string

const (
	OnDeleteRestrict OnDelete = "restrict"
	OnDeleteSetNull  OnDelete = "set_null"
	OnDeleteCascade  OnDelete = "cascade"
)

// AdminView 仅影响后台展示，不参与数据校验和公开策略。
type AdminView struct {
	View string `yaml:"view,omitempty" json:"view,omitempty"`
	// Label 后台显示名; 空 = 回退类型名（配置键）。字段级已有 FieldDef.Label，这里管的是"类型"。
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
	// Group 后台"内容管理"左侧类型列表的分组; 空 = 排在最前（不归入任何分组）。
	Group   string   `yaml:"group,omitempty" json:"group,omitempty"`
	Icon    string   `yaml:"icon,omitempty" json:"icon,omitempty"`
	Columns []string `yaml:"columns,omitempty" json:"columns,omitempty"`
}

// FieldDef 字段定义。代数声明在字段顶层:
// symmetric / transitive / equivalence。
// 边本身双向（OutEdges/InEdges 引擎原语）— 无需 reverse/inverse 声明。
type FieldDef struct {
	Name  string `yaml:"name" json:"name"`
	Label string `yaml:"label" json:"label"` // 显示名（表单/列表）; 空 = 回退字段名
	Kind  string `yaml:"kind" json:"kind"`
	To    string `yaml:"to" json:"to"` // ref/ref[]: 目标类型名
	// 复合字段（结构语法, 非值类型 — 不进 kinds 注册表）:
	// item/fields 只描述结构; 嵌套层不接受 ref/ref[]（引用必须用顶层字段）。
	Options     []string   `yaml:"options,omitempty" json:"options,omitempty"` // kind=select: 可选项
	Item        *FieldDef  `yaml:"item,omitempty" json:"item,omitempty"`       // kind=array: 元素定义（递归）
	Fields      []FieldDef `yaml:"fields,omitempty" json:"fields,omitempty"`   // kind=object: 子字段（递归）
	Required    bool       `yaml:"required" json:"required"`
	Default     any        `yaml:"default,omitempty" json:"default,omitempty"`
	Immutable   bool       `yaml:"immutable,omitempty" json:"immutable,omitempty"`
	Symmetric   bool       `yaml:"symmetric" json:"symmetric"`     // 对称: 存一次查双向
	Transitive  bool       `yaml:"transitive" json:"transitive"`   // 传递: 可达性遍历
	Equivalence bool       `yaml:"equivalence" json:"equivalence"` // 等价类展开
	OnDelete    OnDelete   `yaml:"on_delete,omitempty" json:"on_delete"`
}

// Types 容器: 每站点一个实例, 持有本容器注册的 kind + 类型定义。
type Types struct {
	kinds map[string]Kind
	defs  map[string]TypeDef
}

// New 建容器并默认注册内置 kind。
func New() *Types {
	t := &Types{kinds: map[string]Kind{}}
	for _, k := range defaultKinds() {
		t.kinds[k.Name()] = k
	}
	return t
}

// defaultKinds 内置实现清单（New 时注册; 每个实现一个文件）。
func defaultKinds() []Kind {
	return []Kind{
		stringKind{},
		textKind{},
		richtextKind{},
		numberKind{},
		boolKind{},
		selectKind{},
		timestampKind{},
		imageKind{},
		galleryKind{},
		fileKind{},
		slugKind{},
		refKind{},
		refListKind{},
	}
}

// RegisterKind 注册新字段类型（站点扩展）。重复注册 panic（fail-loud）。
// 必须在 Load 之前调用。
func (t *Types) RegisterKind(k Kind) {
	if k == nil || k.Name() == "" {
		panic("types: register kind: nil or empty name")
	}
	if _, ok := t.kinds[k.Name()]; ok {
		panic(fmt.Sprintf("types: kind %q already registered", k.Name()))
	}
	t.kinds[k.Name()] = k
}

// Kind 取 kind 实现; 不存在返回 ok=false。
func (t *Types) Kind(name string) (Kind, bool) {
	k, ok := t.kinds[name]
	return k, ok
}

// KindNames 已注册 kind 名（字典序，稳定输出）。
func (t *Types) KindNames() []string {
	out := make([]string, 0, len(t.kinds))
	for name := range t.kinds {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Load 解析并整体校验类型定义。非法定义响亮报错。
func (t *Types) Load(raw []byte) error {
	var cfg struct {
		Types map[string]TypeDef `yaml:"types"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return fmt.Errorf("types: parse: %w", err)
	}
	if len(cfg.Types) == 0 {
		return errors.New("types: no types defined")
	}
	// strings 简写归一: array<string>（cmx 同款 — 配置书写便捷）
	for name := range cfg.Types {
		td := cfg.Types[name]
		for i, f := range td.Fields {
			if f.Kind == "strings" {
				f.Kind = KindArray
				f.Item = &FieldDef{Kind: "text"} // string 归一为 array<string> — kind 已改名 text
				td.Fields[i] = f
			}
		}
		cfg.Types[name] = td
	}
	// map key 是类型名: 回填进 TypeDef.Name（yaml 不会自动写）
	for name, td := range cfg.Types {
		td.Name = name
		cfg.Types[name] = td
	}
	defs := cfg.Types
	t.normalizeRelationDefaults(defs)
	if err := t.validate(defs); err != nil {
		return err
	}
	t.defs = defs
	return nil
}

// Type 取类型定义; 不存在返回 ok=false。
func (t *Types) Type(name string) (TypeDef, bool) {
	d, ok := t.defs[name]
	return d, ok
}

// Names 全部类型名（字典序，稳定输出）。
func (t *Types) Names() []string {
	out := make([]string, 0, len(t.defs))
	for name := range t.defs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Defs 全部类型定义（导出副本, admin UI 表单渲染用）。
func (t *Types) Defs() map[string]TypeDef {
	out := make(map[string]TypeDef, len(t.defs))
	for k, v := range t.defs {
		out[k] = v
	}
	return out
}

// Field 取类型字段; 不存在返回 ok=false。
func (t *Types) Field(typeName, fieldName string) (FieldDef, bool) {
	td, ok := t.defs[typeName]
	if !ok {
		return FieldDef{}, false
	}
	for _, f := range td.Fields {
		if f.Name == fieldName {
			return f, true
		}
	}
	return FieldDef{}, false
}

// ── 整体校验（宪法）─────────────────────────────────

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	reserved = map[string]bool{"node": true, "types": true, "settings": true}
	// Node 的真正通用列不得在动态字段中重复声明。
	reservedField = map[string]bool{
		"id": true, "type": true, "display": true, "revision": true,
		"fields": true, "created_at": true, "updated_at": true, "archived_at": true,
	}
	// nodeColumns 节点列全集（穿透路径第二段: 无 $. 前缀即列）。
	nodeColumns = map[string]bool{
		"id": true, "type": true, "display": true, "revision": true,
		"fields": true, "created_at": true, "updated_at": true, "archived_at": true,
	}
)

func (t *Types) validate(defs map[string]TypeDef) error {
	// 容器只做通用校验（类型名/字段名/重复/kind 存在）,
	// 具体 kind 的字段约束由 kind 自己的 ValidateField 裁判。
	for name, td := range defs {
		if !nameRe.MatchString(name) {
			return fmt.Errorf("types: type %q: must match %s", name, nameRe)
		}
		if reserved[name] {
			return fmt.Errorf("types: type %q is reserved", name)
		}
		seen := map[string]bool{}
		for _, f := range td.Fields {
			if reservedField[f.Name] {
				return fmt.Errorf("types: type %q: field %q is reserved for node columns", name, f.Name)
			}
			if !nameRe.MatchString(f.Name) {
				return fmt.Errorf("types: type %q: field %q: must match %s", name, f.Name, nameRe)
			}
			if seen[f.Name] {
				return fmt.Errorf("types: type %q: duplicate field %q", name, f.Name)
			}
			seen[f.Name] = true
			// 复合字段: 结构语法（递归校验, 不进 kinds 注册表 — 非值类型）
			if f.Kind == KindArray || f.Kind == KindObject {
				err := t.validateComposite(name, f.Name, f, defs, 0)
				if err != nil {
					return err
				}
				if f.Default != nil {
					err = t.ValidateValue(name, f, f.Default)
					if err != nil {
						return fmt.Errorf("types: %q.%s default: %w", name, f.Name, err)
					}
				}
				continue
			}
			k, ok := t.kinds[f.Kind]
			if !ok {
				return fmt.Errorf("types: %q.%s: unknown kind %q", name, f.Name, f.Kind)
			}
			if err := k.ValidateField(t, name, f, defs); err != nil {
				return err
			}
			if f.Default != nil {
				if err := t.ValidateValue(name, f, f.Default); err != nil {
					return fmt.Errorf("types: %q.%s default: %w", name, f.Name, err)
				}
			}
		}
		if err := t.validateTypeConfig(name, td); err != nil {
			return err
		}
	}
	return nil
}

func (t *Types) normalizeRelationDefaults(defs map[string]TypeDef) {
	for typeName, td := range defs {
		for i := range td.Fields {
			field := &td.Fields[i]
			kind, ok := t.kinds[field.Kind]
			if !ok || (kind.Class() != ClassRef && kind.Class() != ClassRefList) {
				continue
			}
			if field.OnDelete == "" {
				field.OnDelete = OnDeleteSetNull
				if field.Required {
					field.OnDelete = OnDeleteRestrict
				}
			}
		}
		defs[typeName] = td
	}
}

// FieldByName 按名取字段。
func FieldByName(td TypeDef, name string) (FieldDef, bool) {
	for _, f := range td.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldDef{}, false
}

// IsTree 该类型是否启用树能力。
func (t *Types) IsTree(typeName string) bool {
	td, ok := t.defs[typeName]
	return ok && td.Capabilities.Tree != nil
}

// FieldQueryOps 返回字段 Kind 声明的查询能力。array/object 是结构语法，
// 只支持 exists/missing，因此返回零值。
func (t *Types) FieldQueryOps(field FieldDef) QueryOps {
	if field.Kind == KindArray || field.Kind == KindObject {
		return QueryOps{}
	}
	kind, ok := t.kinds[field.Kind]
	if !ok {
		panic(fmt.Sprintf("types: kind %q not registered", field.Kind))
	}
	return kind.QueryOps()
}

// IsRefKind 该 kind 是否引用系（ClassRef / ClassRefList）。
// 复合字段（array/object）返回 false: 结构语法存 fields JSON, 非引用;
// 嵌套 ref 在 Load 期就被拒绝, 因此不存在“内部带引用的复合字段”。
// 未知 kind panic: Load 已保证字段 kind 存在（复合字段除外）, 未知即程序
// bug（fail-loud, 不静默 — 静默会让 ref 字段被当标量存进 fields, 数据损坏）。
func (t *Types) IsRefKind(kind string) bool {
	if kind == KindArray || kind == KindObject {
		return false // 复合字段: 非引用, 值存 fields JSON
	}
	k, ok := t.kinds[kind]
	if !ok {
		panic(fmt.Sprintf("types: kind %q not registered", kind))
	}
	return k.Class() != ClassField
}

// ── 值校验 ─────────────────────────────────────────

// ValidateValue 校验字段值（写入入口, fail-loud）。
func (t *Types) ValidateValue(typeName string, f FieldDef, v any) error {
	// 复合字段: 结构递归（值判定下沉到叶子 kind）
	switch f.Kind {
	case KindArray:
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("types: %q.%s: expects array, got %T", typeName, f.Name, v)
		}
		elem := *f.Item
		elem.Required = false // 元素的 required 无意义（存在即元素, cmx 同款）
		for i, e := range arr {
			if err := t.ValidateValue(typeName, elem, e); err != nil {
				return fmt.Errorf("types: %q.%s[%d]: %w", typeName, f.Name, i, err)
			}
		}
		return nil
	case KindObject:
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("types: %q.%s: expects object, got %T", typeName, f.Name, v)
		}
		defs := map[string]FieldDef{}
		for _, sub := range f.Fields {
			defs[sub.Name] = sub
		}
		for subName, subV := range obj {
			sub, ok := defs[subName]
			if !ok {
				return fmt.Errorf("types: %q.%s: unknown sub-field %q", typeName, f.Name, subName)
			}
			if err := t.ValidateValue(typeName, sub, subV); err != nil {
				return err
			}
		}
		// 子字段 required（cmx validateFields 递归同款语义）
		for _, sub := range f.Fields {
			if !sub.Required {
				continue
			}
			if subV, ok := obj[sub.Name]; !ok || t.isEmpty(sub.Kind, subV) {
				return fmt.Errorf("types: %q.%s: required sub-field %q missing", typeName, f.Name, sub.Name)
			}
		}
		return nil
	}
	k, ok := t.kinds[f.Kind]
	if !ok {
		return fmt.Errorf("types: %q.%s: unknown kind %q", typeName, f.Name, f.Kind)
	}
	if err := k.Validate(f, v); err != nil {
		return fmt.Errorf("types: %q.%s: %w", typeName, f.Name, err)
	}
	return nil
}

// ValidateFields 校验整组字段（类型定义已知的字段; 未知字段拒绝 — 防拼写错误）。
func (t *Types) ValidateFields(typeName string, fields map[string]any) error {
	td, ok := t.defs[typeName]
	if !ok {
		return fmt.Errorf("types: type %q not defined", typeName)
	}
	defs := map[string]FieldDef{}
	for _, f := range td.Fields {
		defs[f.Name] = f
	}
	for name, v := range fields {
		f, ok := defs[name]
		if !ok {
			return fmt.Errorf("types: %q: unknown field %q", typeName, name)
		}
		if err := t.ValidateValue(typeName, f, v); err != nil {
			return err
		}
	}
	// required 检查（空值视为缺）
	for _, f := range td.Fields {
		if !f.Required {
			continue
		}
		v, ok := fields[f.Name]
		if !ok || t.isEmpty(f.Kind, v) {
			return fmt.Errorf("types: %q: required field %q missing", typeName, f.Name)
		}
	}
	return nil
}

// ValidatePatchFields 校验差量字段。nil 表示删除/清空：可选字段允许，
// required 字段拒绝；非 nil 值与 Create 使用同一个 Kind 校验。
func (t *Types) ValidatePatchFields(typeName string, fields map[string]any) error {
	td, ok := t.defs[typeName]
	if !ok {
		return fmt.Errorf("types: type %q not defined", typeName)
	}
	for name, value := range fields {
		field, ok := FieldByName(td, name)
		if !ok {
			return fmt.Errorf("types: %q: unknown field %q", typeName, name)
		}
		if field.Immutable {
			return fmt.Errorf("types: %q: immutable field %q cannot be patched", typeName, name)
		}
		if value == nil {
			if field.Required {
				return fmt.Errorf("types: %q: required field %q cannot be deleted", typeName, name)
			}
			continue
		}
		if err := t.ValidateValue(typeName, field, value); err != nil {
			return err
		}
		if field.Required && t.isEmpty(field.Kind, value) {
			return fmt.Errorf("types: %q: required field %q cannot be empty", typeName, name)
		}
	}
	return nil
}

// ── 复合字段（array/object 结构语法, 非值类型 — 不进 kinds 注册表）──

// maxCompositeDepth 复合字段嵌套上限（防畸形配置）。
const maxCompositeDepth = 4

// validateComposite 递归校验复合字段: array 必带 item; object 必带 fields;
// 递归到叶子时校验 kind 存在、没有引用代数声明, 并执行该 kind 自己的字段约束。
//
// 复合结构内不允许引用系 kind（ref/ref[]）: 嵌套引用没有路径可落 Edge,
// 若降级成标量存进 fields JSON, 就只剩裸 ID — 没有外键、基数、删除策略,
// 完整性检查也看不到。需要“数组/对象里带引用”时请建模为关系 Node。
//
// path 是相对路径（如 members[].person）, 嵌套层没有自己的字段名,
// 用它生成可定位的错误。
func (t *Types) validateComposite(typeName, path string, f FieldDef, defs map[string]TypeDef, depth int) error {
	if depth > maxCompositeDepth {
		return fmt.Errorf("types: type %q field %q: nesting depth exceeds %d", typeName, path, maxCompositeDepth)
	}
	decl := f
	decl.Name = path
	switch f.Kind {
	case KindArray:
		if err := rejectRefAttrs(typeName, decl); err != nil {
			return err
		}
		if f.Item == nil {
			return fmt.Errorf("types: type %q field %q: array requires item definition", typeName, path)
		}
		if len(f.Fields) > 0 {
			return fmt.Errorf("types: type %q field %q: array must not declare fields", typeName, path)
		}
		return t.validateComposite(typeName, path+"[]", *f.Item, defs, depth+1)
	case KindObject:
		if err := rejectRefAttrs(typeName, decl); err != nil {
			return err
		}
		if len(f.Fields) == 0 {
			return fmt.Errorf("types: type %q field %q: object requires fields", typeName, path)
		}
		if f.Item != nil {
			return fmt.Errorf("types: type %q field %q: object must not declare item", typeName, path)
		}
		seen := make(map[string]bool, len(f.Fields))
		for _, sub := range f.Fields {
			if !nameRe.MatchString(sub.Name) {
				return fmt.Errorf("types: type %q field %q.%s: must match %s", typeName, path, sub.Name, nameRe)
			}
			if seen[sub.Name] {
				return fmt.Errorf("types: type %q field %q: duplicate sub-field %q", typeName, path, sub.Name)
			}
			seen[sub.Name] = true
			if err := t.validateComposite(typeName, path+"."+sub.Name, sub, defs, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	kind, ok := t.kinds[f.Kind]
	if !ok {
		return fmt.Errorf("types: type %q field %q: unknown kind %q", typeName, path, f.Kind)
	}
	if kind.Class() != ClassField {
		return fmt.Errorf(
			"types: type %q field %q: kind %s cannot be nested in array/object; use a top-level ref field or a relation Node",
			typeName, path, f.Kind)
	}
	if err := rejectRefAttrs(typeName, decl); err != nil {
		return err
	}
	return kind.ValidateField(t, typeName, decl, defs)
}

// isEmpty 空判断（required 检查用）: 复合字段按形状, 标量走 kind.IsEmpty。
func (t *Types) isEmpty(kind string, v any) bool {
	switch kind {
	case KindArray:
		arr, ok := v.([]any)
		return !ok || len(arr) == 0
	case KindObject:
		obj, ok := v.(map[string]any)
		return !ok || len(obj) == 0
	}
	k, ok := t.kinds[kind]
	if !ok {
		return true
	}
	return k.IsEmpty(v)
}
