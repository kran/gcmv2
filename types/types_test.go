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
    url: /article/{slug}
    search: true
    fields:
      - { name: body, kind: richtext, required: true }
      - { name: cover, kind: upload-image }
      - { name: authors, kind: "ref[]", to: person }
      - { name: related, kind: "ref[]", to: article, symmetric: true }
      - { name: categories, kind: "ref[]", to: category, transitive: true }
  category:
    url: /category/{slug}
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
	if !td.Search {
		t.Fatalf("article search: %+v", td)
	}
	if got := td.TemplateCandidates(); len(got) != 2 || got[0] != "node--article.html" {
		t.Fatalf("candidates: %v", got)
	}
	// 引用字段代数
	f, ok := ts.Field("article", "related")
	if !ok || !f.Symmetric {
		t.Fatal("related symmetric missing")
	}
	if f, ok := ts.Field("category", "parent"); !ok || !f.Transitive {
		t.Fatal("parent transitive missing")
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
	}
	// url 字段框架层已移除 — yaml 中出现被忽略（不报错）
	ts := New()
	if err := ts.Load([]byte("types:\n  article:\n    url: /bad space/{slug}\n    fields: []")); err != nil {
		t.Fatalf("url field must be ignored: %v", err)
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

// view: tree 校验 — 有自引用 ref 通过, 无则 fail-loud。
func TestViewTree(t *testing.T) {
	// 合法: category 有 parent 自引用 + view: tree
	ts := New()
	if err := ts.Load([]byte(`
types:
  category:
    view: tree
    fields:
      - { name: name, kind: text }
      - { name: parent, kind: ref, to: category }
`)); err != nil {
		t.Fatalf("valid tree: %v", err)
	}
	if !ts.IsTree("category") {
		t.Fatal("IsTree must be true")
	}
	// 非法: view: tree 无自引用
	ts2 := New()
	err := ts2.Load([]byte(`
types:
  article:
    view: tree
    fields:
      - { name: title, kind: text }
`))
	if err == nil || !strings.Contains(err.Error(), "requires a self-ref") {
		t.Fatalf("tree without self-ref must fail: %v", err)
	}
	// 默认 list
	ts3 := New()
	if err := ts3.Load([]byte(`
types:
  person:
    fields:
      - { name: name, kind: text }
`)); err != nil {
		t.Fatal(err)
	}
	if ts3.IsTree("person") {
		t.Fatal("default view must be list")
	}
}

// title 穿透声明: 合法/非法校验。
func TestTitleThrough(t *testing.T) {
	good := `
types:
  person:
    title: name
    fields:
      - { name: name, kind: text }
  employment:
    title: person.$.name
    fields:
      - { name: person, kind: ref, to: person }
`
	bad := []struct{ name, raw string }{
		{"ref 不存在", `
types:
  person: { fields: [ { name: name, kind: text } ] }
  employment:
    title: ghost.$.name
    fields: [ { name: person, kind: ref, to: person } ]`},
		{"第一段非 ref", `
types:
  person: { fields: [ { name: name, kind: text } ] }
  employment:
    title: role.$.name
    fields: [ { name: role, kind: text } ]`},
		{"目标字段不存在", `
types:
  person: { fields: [ { name: name, kind: text } ] }
  employment:
    title: person.$.ghost
    fields: [ { name: person, kind: ref, to: person } ]`},
		{"目标非标量", `
types:
  person: { fields: [ { name: buddy, kind: ref, to: person } ] }
  employment:
    title: person.$.buddy
    fields: [ { name: person, kind: ref, to: person } ]`},
		{"第二段既非 $ 也非列", `
types:
  person: { fields: [ { name: name, kind: text } ] }
  employment:
    title: person.xyz
    fields: [ { name: person, kind: ref, to: person } ]`},
	}
	ts := New()
	if err := ts.Load([]byte(good)); err != nil {
		t.Fatalf("good title path must load: %v", err)
	}
	for _, c := range bad {
		if err := New().Load([]byte(c.raw)); err == nil {
			t.Fatalf("%s: must fail", c.name)
		}
	}
}

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
    title: title
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
