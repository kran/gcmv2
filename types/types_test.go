package types

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// 合法完整定义: 文章↔专家双向引用、相关对称、分类树传递、归属、关系节点。
const validYAML = `
types:
  article:
    capabilities:
      searchable: { fields: [display, body] }
    fields:
      - { name: body, kind: richtext, required: true }
      - { name: cover, kind: upload-image }
      - { name: authors, kind: "ref[]", to: person }
      - { name: related, kind: "ref[]", to: article, symmetric: true }
      - { name: categories, kind: "ref[]", to: category }
  category:
    fields:
      - { name: name, kind: text, required: true }
      - { name: parent, kind: ref, to: category, transitive: true }

      - { name: banner, kind: upload-image }
  person:
    fields:
      - { name: name, kind: text, required: true }
      - { name: articles, kind: "ref[]", to: article }
      - { name: employment, kind: "ref[]", to: employment }
  org:
    fields:
      - { name: name, kind: text, required: true }
  employment:
    capabilities:
      relation: { from: person, to: org }
    fields:
      - { name: person, kind: ref, to: person, required: true }
      - { name: org, kind: ref, to: org, required: true }
      - { name: role, kind: text }
`

func TestLoadValid(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(validYAML)); err != nil {
		t.Fatalf("Load: %v", err)
	}
	td, ok := ts.Type("article")
	if !ok {
		t.Fatal("article type missing")
	}
	if td.Capabilities.Searchable == nil {
		t.Fatalf("article searchable capability: %+v", td)
	}
	if got := td.TemplateCandidates(); len(got) != 2 || got[0] != "node--article.html" {
		t.Fatalf("candidates: %v", got)
	}
	// 引用字段代数
	f, ok := ts.Field("article", "related")
	if !ok || !f.Symmetric {
		t.Fatal("related symmetric missing")
	}
	if f, ok := ts.Field("category", "parent"); !ok || !f.Transitive || f.OnDelete != OnDeleteSetNull {
		t.Fatalf("parent relation metadata = %#v", f)
	}
	if f, ok := ts.Field("employment", "person"); !ok || f.OnDelete != OnDeleteRestrict {
		t.Fatalf("required ref default on_delete = %#v", f)
	}
	if relation, ok := ts.Relation("employment"); !ok || relation.From != "person" || relation.To != "org" {
		t.Fatalf("relation capability = %#v", relation)
	}
	if len(ts.Names()) != 5 {
		t.Fatalf("names: %v", ts.Names())
	}
}

// 非法定义表驱动: 每种错误类型一个用例, 必须 fail-loud。
func TestLoadInvalid(t *testing.T) {
	base := "types:\n  article:\n    fields:\n"
	cases := []struct {
		name string
		yaml string
		want string // 错误信息片段
	}{
		{"empty", "types: {}", "no types"},
		{"type name", base + "      - { name: body, kind: richtext }\n  BadType:\n    fields: []", "must match"},
		{"reserved", base + "      - { name: body, kind: richtext }\n  node:\n    fields: []", "reserved"},
		{"unknown kind", base + "      - { name: body, kind: banana }", "unknown kind"},
		{"bad field name", base + "      - { name: 'Bad-Name', kind: textarea }", "must match"},
		{"duplicate field", base + "      - { name: body, kind: textarea }\n      - { name: body, kind: textarea }", "duplicate"},
		{"ref no to", base + "      - { name: authors, kind: ref }", "requires to"},
		{"ref to undefined", base + "      - { name: authors, kind: ref, to: ghost }", "not defined"},
		{"algebra mutual", base + "      - { name: r, kind: \"ref[]\", to: article, symmetric: true, transitive: true }",
			"mutually exclusive"},
		{"algebra cross type", base + "      - { name: r, kind: ref, to: other, transitive: true }\n  other:\n    fields: []", "self reference"},
		{"required set null", base + "      - { name: r, kind: ref, to: article, required: true, on_delete: set_null }", "required ref cannot use set_null"},
		{"cascade without relation", base + "      - { name: r, kind: ref, to: article, required: true, on_delete: cascade }", "requires a relation endpoint"},
	}
	// 未知配置必须 fail-loud，不能静默忽略拼写错误或已移除字段。
	ts := New()
	if err := ts.Load([]byte("types:\n  article:\n    url: /bad space/{slug}\n    fields: []")); err == nil || !strings.Contains(err.Error(), "field url not found") {
		t.Fatalf("unknown type property must fail: %v", err)
	}
	if err := ts.Load([]byte("types:\n  article:\n    fields:\n      - { name: body, kind: text, mystery: true }")); err == nil || !strings.Contains(err.Error(), "field mystery not found") {
		t.Fatalf("unknown field property must fail: %v", err)
	}
	for _, c := range cases {
		ts := New()
		err := ts.Load([]byte(c.yaml))
		if err == nil {
			t.Fatalf("must fail for %q", c.name)
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Fatalf("error %q does not contain %q", err, c.want)
		}
	}
}

// 值校验: 合法值通过, 非法值拒绝。
func TestRelationCapabilityValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing endpoint",
			yaml: `types:
  person: { fields: [] }
  employment:
    capabilities: { relation: { from: person, to: account } }
    fields:
      - { name: person, kind: ref, to: person, required: true }
`,
			want: `field "account" is not defined`,
		},
		{
			name: "endpoint must be required single ref",
			yaml: `types:
  person: { fields: [] }
  employment:
    capabilities: { relation: { from: people, to: owner } }
    fields:
      - { name: people, kind: "ref[]", to: person, required: true }
      - { name: owner, kind: ref, to: person, required: true }
`,
			want: "required single ref",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			typeSet := New()
			err := typeSet.Load([]byte(test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateValue(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(validYAML)); err != nil {
		t.Fatal(err)
	}
	field := func(typ, name string) FieldDef {
		f, ok := ts.Field(typ, name)
		if !ok {
			t.Fatalf("field %s.%s missing", typ, name)
		}
		return f
	}
	// 合法
	okCases := []struct {
		typ, name string
		v         any
	}{
		{"article", "body", "<p>hi</p>"},
		{"article", "cover", "/uploads/x.png"},
		{"article", "authors", []any{int64(1), int64(2)}},
		{"employment", "person", int64(3)},
	}
	for _, c := range okCases {
		if err := ts.ValidateValue(c.typ, field(c.typ, c.name), c.v); err != nil {
			t.Fatalf("%s.%s=%v: %v", c.typ, c.name, c.v, err)
		}
	}
	// 非法
	badCases := []struct {
		typ, name string
		v         any
		want      string
	}{
		{"article", "body", 123, "expects string"},
		{"article", "authors", "not-array", "expects array"},
		{"article", "authors", []any{"str"}, "expects node id"},
		{"article", "authors", []any{1.5}, "integer"},
		{"employment", "person", "x", "expects node id"},
	}
	for _, c := range badCases {
		err := ts.ValidateValue(c.typ, field(c.typ, c.name), c.v)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s.%s=%v: err=%v want %q", c.typ, c.name, c.v, err, c.want)
		}
	}
}

// ValidateFields 整组校验: 未知字段拒绝 + required 检查。
func TestValidateFields(t *testing.T) {
	ts := New()
	_ = ts.Load([]byte(validYAML))
	// 未知字段
	err := ts.ValidateFields("article", map[string]any{"body": "x", "ghost": 1})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field must fail: %v", err)
	}
	// required 缺失
	err = ts.ValidateFields("article", map[string]any{"cover": "/x.png"})
	if err == nil || !strings.Contains(err.Error(), "required field") {
		t.Fatalf("required must fail: %v", err)
	}
	// required ref 缺失
	err = ts.ValidateFields("employment", map[string]any{"role": "秘书长"})
	if err == nil || !strings.Contains(err.Error(), "required field") {
		t.Fatalf("required ref must fail: %v", err)
	}
	// 合法
	if err := ts.ValidateFields("article", map[string]any{"body": "x"}); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

// ── 站点扩展: RegisterKind ─────────────────────────

// dateKind 站点自定义 kind 示例: RFC3339 日期字符串。
type dateKind struct{}

func (dateKind) Name() string { return "date" }
func (dateKind) Validate(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("expects RFC3339 string, got %T", v)
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		return fmt.Errorf("invalid RFC3339 date %q", s)
	}
	return nil
}
func (dateKind) IsEmpty(v any) bool { s, ok := v.(string); return !ok || s == "" }
func (dateKind) Class() Class       { return ClassField }
func (dateKind) QueryOps() QueryOps {
	return QueryOps{Equal: true, Ordered: true, Sortable: true}
}
func (dateKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}

func TestRegisterKind(t *testing.T) {
	ts := New()
	ts.RegisterKind(dateKind{})
	if _, ok := ts.Kind("date"); !ok {
		t.Fatal("date kind missing")
	}
	// 使用自定义 kind 的类型定义
	cfg := "types:\n  employment:\n    fields:\n      - { name: start_date, kind: date, required: true }\n"
	if err := ts.Load([]byte(cfg)); err != nil {
		t.Fatalf("Load with custom kind: %v", err)
	}
	f, _ := ts.Field("employment", "start_date")
	operations := ts.FieldQueryOps(f)
	if !operations.Equal || !operations.Ordered || !operations.Sortable || operations.Text {
		t.Fatalf("custom date query operations = %#v", operations)
	}
	if err := ts.ValidateValue("employment", f, "2024-01-15T00:00:00Z"); err != nil {
		t.Fatalf("valid date: %v", err)
	}
	if err := ts.ValidateValue("employment", f, "not-a-date"); err == nil {
		t.Fatal("invalid date must fail")
	}
	// required 检查走自定义 IsEmpty
	if err := ts.ValidateFields("employment", map[string]any{}); err == nil ||
		!strings.Contains(err.Error(), "required field") {
		t.Fatalf("required date must fail: %v", err)
	}
}

// 未注册 kind 的容器: Load 必须 fail-loud。
func TestLoadUnknownKind(t *testing.T) {
	ts := New() // 未注册 date
	cfg := "types:\n  x:\n    fields:\n      - { name: d, kind: date }\n"
	err := ts.Load([]byte(cfg))
	if err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("unknown kind must fail: %v", err)
	}
}

// 重复注册 panic（fail-loud）。
func TestRegisterDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate register must panic")
		}
	}()
	ts := New()
	ts.RegisterKind(stringKind{})
}

// tree capability 校验：parent 必须是自引用；后台 tree view 依赖该能力。
func TestTreeCapability(t *testing.T) {
	ts := New()
	if err := ts.Load([]byte(`
types:
  category:
    capabilities:
      tree: { parent: parent, order: position }
    admin: { view: tree }
    fields:
      - { name: name, kind: text }
      - { name: position, kind: number }
      - { name: parent, kind: ref, to: category }
`)); err != nil {
		t.Fatalf("valid tree: %v", err)
	}
	if !ts.IsTree("category") {
		t.Fatal("IsTree must be true")
	}

	ts2 := New()
	err := ts2.Load([]byte(`
types:
  article:
    capabilities:
      tree: { parent: title }
    fields:
      - { name: title, kind: text }
`))
	if err == nil || !strings.Contains(err.Error(), "must be a single self ref") {
		t.Fatalf("tree without self-ref must fail: %v", err)
	}
}

// title 穿透声明: 合法/非法校验。
// slug 约束: 字母开头 / 白名单字符 / 禁止连续 --。
func TestValidSlug(t *testing.T) {
	valid := []string{"ai", "ai-industry", "page1", "a_b", "a-1-b", "A-B"}
	invalid := []string{"", "1abc", "-abc", "_abc", "a--b", "a b", "a/b", "a..b", "a--", "-"}
	for _, s := range valid {
		if !ValidSlug(s) {
			t.Fatalf("valid slug %q rejected", s)
		}
	}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Fatalf("invalid slug %q accepted", s)
		}
	}
}

// 复合字段（array/object）: 定义校验 + 值递归校验 — Kind 接口不动的验证。
func TestCompositeFields(t *testing.T) {
	raw := `
types:
  page:
    fields:
      - { name: title, kind: text }
      - { name: tags, kind: array, item: { kind: textarea } }
      - { name: nav, kind: array, item: { kind: object, fields:
            [ { name: label, kind: text }, { name: url, kind: text } ] } }
      - { name: meta, kind: object, fields: [ { name: og_title, kind: textarea } ] }
`
	ts := New()
	if err := ts.Load([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	// 值校验: 合法
	fields := map[string]any{
		"title": "t",
		"tags":  []any{"a", "b"},
		"nav":   []any{map[string]any{"label": "首页", "url": "/"}},
		"meta":  map[string]any{"og_title": "x"},
	}
	if err := ts.ValidateFields("page", fields); err != nil {
		t.Fatalf("valid composite: %v", err)
	}
	// 非法: 元素类型错 / 未知子字段
	if err := ts.ValidateFields("page", map[string]any{"tags": []any{"a", 5}}); err == nil {
		t.Fatal("tag element must be string")
	}
	if err := ts.ValidateFields("page", map[string]any{"meta": map[string]any{"ghost": 1}}); err == nil {
		t.Fatal("unknown sub-field must fail")
	}
	// 定义校验: array 缺 item / object 缺 fields / 深度超限
	if err := New().Load([]byte(`
types:
  bad1: { fields: [ { name: a, kind: array } ] }`)); err == nil {
		t.Fatal("array without item must fail")
	}
	if err := New().Load([]byte(`
types:
  bad2: { fields: [ { name: a, kind: object } ] }`)); err == nil {
		t.Fatal("object without fields must fail")
	}
}

// cmx 语义补齐: strings 归一 + object 子字段 required + array 元素 required 忽略。
func TestCompositeCmxSemantics(t *testing.T) {
	raw := `
types:
  page:
    fields:
      - { name: tags, kind: strings }  # 简写 → array<string>（kind 改名后为 array<text>）
      - { name: meta, kind: object, fields:
            [ { name: og_title, kind: text, required: true } ] }
`
	ts := New()
	if err := ts.Load([]byte(raw)); err != nil {
		t.Fatal(err)
	}
	// strings 归一: kind 变 array
	td, _ := ts.Type("page")
	if td.Fields[0].Kind != "array" || td.Fields[0].Item == nil || td.Fields[0].Item.Kind != "text" {
		t.Fatalf("strings must normalize to array<text>: %+v", td.Fields[0])
	}
	// object 子字段 required 缺 → 拒绝
	if err := ts.ValidateFields("page", map[string]any{"tags": []any{"a"}, "meta": map[string]any{}}); err == nil {
		t.Fatal("required sub-field must be enforced")
	}
	// 合法
	if err := ts.ValidateFields("page", map[string]any{"tags": []any{"a"}, "meta": map[string]any{"og_title": "x"}}); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

// select kind: options 必填/去重; 值必须在 options 内。
func TestCapabilitiesDefaultsAndImmutable(t *testing.T) {
	ts := New()
	err := ts.Load([]byte(`
types:
  article:
    capabilities:
      searchable: { fields: [display, title] }
      addressable: { field: slug, unique: global }
      publication: { field: state, draft: draft, published: published }
    constraints:
      unique: [[external_id]]
      indexes: [[state, position]]
    admin: { view: list, columns: [slug, state, updated_at] }
    fields:
      - { name: title, kind: text, required: true }
      - { name: slug, kind: slug }
      - { name: state, kind: select, options: [draft, published], default: draft, required: true }
      - { name: position, kind: number, default: 0 }
      - { name: external_id, kind: text, immutable: true }
`))
	if err != nil {
		t.Fatal(err)
	}
	fields, err := ts.ApplyDefaults("article", map[string]any{"title": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if fields["state"] != "draft" || fields["position"] != 0 {
		t.Fatalf("defaults = %#v", fields)
	}
	if err := ts.ValidateFields("article", fields); err != nil {
		t.Fatal(err)
	}
	if err := ts.ValidatePatchFields("article", map[string]any{"external_id": "changed"}); err == nil {
		t.Fatal("immutable field patch must fail")
	}
	if !ts.IsPublished("article", map[string]any{"state": "published"}) {
		t.Fatal("published capability did not match")
	}
	if got := ts.Address("article", map[string]any{"slug": "hello"}); got != "hello" {
		t.Fatalf("address = %q", got)
	}
}

func TestCapabilityValidation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{"address kind", `types: { article: { capabilities: { addressable: { field: title, unique: global } }, fields: [ { name: title, kind: text } ] } }`, "must use kind"},
		{"publication field", `types: { article: { capabilities: { publication: { field: state, draft: draft, published: published } }, fields: [ { name: title, kind: text } ] } }`, "not defined"},
		{"tree parent", `types: { category: { capabilities: { tree: { parent: name } }, fields: [ { name: name, kind: text } ] } }`, "self ref"},
		{"ref index", `types: { a: { constraints: { indexes: [[owner]] }, fields: [ { name: owner, kind: ref, to: b } ] }, b: { fields: [] } }`, "cannot be a ref"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := New().Load([]byte(test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSelectKind(t *testing.T) {
	ts := New()
	// 无 options 拒绝
	if err := ts.Load([]byte("types:\n  x:\n    fields:\n      - { name: t, kind: select }\n")); err == nil {
		t.Fatal("select 无 options 应拒绝")
	}
	// 合法
	ts2 := New()
	if err := ts2.Load([]byte("types:\n  x:\n    fields:\n      - { name: t, kind: select, options: [a, b] }\n")); err != nil {
		t.Fatal(err)
	}
	// 值在 options 内 ✓
	if err := ts2.ValidateFields("x", map[string]any{"t": "a"}); err != nil {
		t.Fatal(err)
	}
	// 值不在 options 内 ✗
	if err := ts2.ValidateFields("x", map[string]any{"t": "c"}); err == nil {
		t.Fatal("值不在 options 应拒绝")
	}
}
