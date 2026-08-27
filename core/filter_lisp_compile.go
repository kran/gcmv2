package core

// Lisp filter 编译器（var 链构建 — dba 结构层递归）。
//
// 与手拼片段 + 全局占位符编号（phGen）不同: 每个子表达式注册为 dba 的
// var 节点（${eN} 引用, 嵌套递归）— 参数在各自 varNode 里, #{n} 从自己
// args 数 — 穿透/逻辑的参数顺序与编号由 dba 结构层管理, 不再手动对齐。
//
//	内层: q.Var("e2", `EXISTS(... WHERE field = #{1} ...)`, seg)
//	外层: q.Var("e1", `EXISTS(... WHERE field = #{1} ... ${e2})`, seg)
//	顶层: q.Add(top) → q.ToSQL() → (where, args)
//
// 构建起点 = s.db（var 注册 copy-on-write — 原库不受影响）。
//
// 语法（2026-08 定案）:
//
//	取值:    status(列) / $label(JSON) / ->categories(出边) / <-comments(入边)
//	数组:    [1 2 3] 字面量 / {:args} 占位符
//	引用:    (edge 字段 目标) — 一元=存在性; 目标=值(折叠)/谓词(开层)
//	集合:    (in 字段 集合) — 字段=列(标量 IN)/引用(边 EXISTS)
//	谓词:    比较 = != > < like / 逻辑 and or not / edge / in

import (
	"fmt"
	"strings"

	"github.com/kran/dba"
)

// lispCompiler 编译状态: var 链 + 编号 + 宿主/节点上下文。
type lispCompiler struct {
	svc     *Service
	q       *dba.SQL
	varSeq  int
	nodeRef string // 列引用的表别名: "nodes" 顶层; edge 开层 = "t{i}"
	link    string // 边查询的链接列来源: "nodes.id" 顶层; 开层 = "t{i}.id"
	depth   int    // edge 开层层级（别名 e{i}/t{i}）
	params  map[string]any
}

// LispFuncC 函数编译器（var 槽版）: 返回片段（${eN} 引用或字面量）。
// 站点 RegisterLispFuncC 注册自定义函数（注册驱动 — 函数进表达式）。
type LispFuncC func(args []lispExpr) (string, []any, error)

// RegisterLispFuncC 注册自定义 Lisp filter 函数（站点扩展; 重复注册 panic）。
func (s *Service) RegisterLispFuncC(name string, fn LispFuncC) {
	if _, dup := s.lispFuncsC[name]; dup {
		panic(fmt.Sprintf("core: lisp func %q already registered", name))
	}
	s.lispFuncsC[name] = fn
}

// CompileLispInto 在 q 上挂载 ${where} var（嵌套 var 同实例 — 原样保留,
// 不 ToSQL）。q 是调用方的 dba 链（base SQL 模板含 ${where} 槽）;
// 返回挂好 var 的 q, 调用方继续链式操作, 最终执行时统一展开。
// CompileLispInto 在 q 上挂载 ${where} var（嵌套 var 同实例 — 原样保留,
// 不 ToSQL）。不做字段校验（宽松编译 — 错误延迟到 SQL 执行 fail-loud）;
// 类型过滤由调用方构建（(= type "x")）。q 是调用方的 dba 链（base SQL
// 模板含 ${where} 槽）; 返回挂好 var 的 q, 调用方继续链式操作。
func (s *Service) CompileLispInto(q *dba.SQL, expr string, params map[string]any) (*dba.SQL, error) {
	e, err := parseLisp(expr)
	if err != nil {
		return nil, err
	}
	c := &lispCompiler{svc: s, q: q, nodeRef: "nodes", link: "nodes.id", params: params}
	if e.head == "" {
		return nil, fmt.Errorf("filter-lisp: top-level must be a call")
	}
	fn, ok := c.funcs()[e.head]
	if !ok {
		return nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
	top, extraArgs, err := fn(e.args)
	if err != nil {
		return nil, err
	}
	// 顶层: ${where} 槽 — var 挂载（片段 + 顶层函数的直接参数）
	return c.q.Var("where", top, extraArgs...), nil
}

// varRef 注册片段为 ${eN}, 返回引用（参数独立编号）。
func (c *lispCompiler) varRef(frag string, args ...any) string {
	c.varSeq++
	name := fmt.Sprintf("e%d", c.varSeq)
	c.q = c.q.Var(name, frag, args...)
	return fmt.Sprintf("${%s}", name)
}

// funcs 函数表: 内置（注册驱动形态）+ 站点注册合并。
func (c *lispCompiler) funcs() map[string]LispFuncC {
	m := map[string]LispFuncC{}
	for _, op := range []string{"=", "!=", ">", ">=", "<", "<=", "like"} {
		op := op
		m[op] = wrap(func(args []lispExpr) (string, error) { return c.cmp(op, args) })
	}
	m["and"] = wrap(c.andFn)
	m["or"] = wrap(c.orFn)
	m["not"] = wrap(c.notFn)
	m["edge"] = wrap(c.edgeFn)
	m["in"] = wrap(c.inFn)
	m["subtree"] = wrap(c.subtreeFn)
	// 站点注册函数（覆盖内置? 不 — 重复注册 panic; 合并）
	for k, v := range c.svc.lispFuncsC {
		m[k] = v
	}
	return m
}

// wrap 单值片段 → (frag, nil)（参数已挂 var; 站点函数可返回 (frag, args)）。
func wrap(fn func(args []lispExpr) (string, error)) LispFuncC {
	return func(args []lispExpr) (string, []any, error) {
		frag, err := fn(args)
		return frag, nil, err
	}
}

// sqlOp 操作符 → SQL（like 保持; 其余原样）。
func sqlOp(op string) string {
	if op == "like" {
		return "LIKE"
	}
	return op
}

// call 编译子表达式（注册表查找; 返回片段 + 直接参数）。
// 函数是黑盒: 调用前后保存/恢复宿主上下文（edge 等会临时切换 td/nodeRef/link,
// 不得泄漏到同层后续表达式）。
func (c *lispCompiler) call(e lispExpr) (string, []any, error) {
	if e.head == "" {
		return "", nil, fmt.Errorf("filter-lisp: expected call, got %v", e.atom)
	}
	fn, ok := c.funcs()[e.head]
	if !ok {
		return "", nil, fmt.Errorf("filter-lisp: unknown function %q", e.head)
	}
	origRef, origLink := c.nodeRef, c.link
	frag, args, err := fn(e.args)
	c.nodeRef, c.link = origRef, origLink
	return frag, args, err
}

// valueOf 原子/占位符 → 参数值。
func (c *lispCompiler) valueOf(v lispExpr) (any, error) {
	if v.head != "" {
		return nil, fmt.Errorf("filter-lisp: expected value, got (%s ...)", v.head)
	}
	return valueParam(v.atom, c.params)
}

func pathOfC(v lispExpr) (string, error) {
	if v.head != "" {
		return "", fmt.Errorf("filter-lisp: expected path, got (%s ...)", v.head)
	}
	p, ok := v.atom.(string)
	if !ok {
		return "", fmt.Errorf("filter-lisp: path must be string")
	}
	return p, nil
}

// fieldKind 字段类别: 列 / JSON / 出边引用 / 入边引用。
type fieldKind int

const (
	fieldCol fieldKind = iota
	fieldJSON
	fieldOut
	fieldIn
)

// fieldOf 解析字段 token 的前缀:
//
//	无前缀 = 列       status
//	$name  = JSON     $label（兼容 $.label）
//	->name = 出边引用  ->categories
//	<-name = 入边引用  <-comments
func fieldOf(path string) (kind fieldKind, typeName, name string) {
	switch {
	case strings.HasPrefix(path, "->"):
		return fieldOut, "", strings.TrimPrefix(path, "->")
	case strings.HasPrefix(path, "<-"):
		// <-type.field — 入边身份 = 来源类型.字段
		rest := strings.TrimPrefix(path, "<-")
		if i := strings.IndexByte(rest, '.'); i >= 0 {
			return fieldIn, rest[:i], rest[i+1:]
		}
		return fieldIn, "", rest
	case strings.HasPrefix(path, "$"):
		// $name — 严格形态; $.name 报错（name 含点, 字段查不到）
		return fieldJSON, "", strings.TrimPrefix(path, "$")
	}
	return fieldCol, "", path
}

// jsonPath $name → JSON 提取路径（$.name）。
func jsonPath(name string) string {
	return "$." + name
}

// ── 函数实现 ─────────────────────────────────────

// cmp 比较: 列/JSON（nodeRef 相对当前节点）。引用字段直接比较 → 报错提示。
func (c *lispCompiler) cmp(op string, args []lispExpr) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("filter-lisp: %s takes 2 args", op)
	}
	path, err := pathOfC(args[0])
	if err != nil {
		return "", err
	}
	val, err := c.valueOf(args[1])
	if err != nil {
		return "", err
	}
	kind, _, name := fieldOf(path)
	switch kind {
	case fieldJSON:
		// 严格 $name（$.name 语法拒绝）; 字段本身不校验（宽松）
		if name == "" || strings.HasPrefix(name, ".") {
			return "", fmt.Errorf("filter-lisp: strict $name (no dot), got %q", path)
		}
		return c.varRef("json_extract("+c.nodeRef+".fields, #{1}) "+sqlOp(op)+" #{2}", jsonPath(name), val), nil
	case fieldCol:
		// 列不校验（宽松 — 拼写错误延迟到 SQL 执行 fail-loud）
		return c.varRef(c.nodeRef+".#{1|quote} "+sqlOp(op)+" #{2}", name, val), nil
	case fieldOut, fieldIn:
		return "", fmt.Errorf("filter-lisp: ref field %q needs edge/in, not %s", name, op)
	}
	return "", fmt.Errorf("filter-lisp: %q neither column nor $.field", path)
}

// and/or/not: 组合子表达式（每个子表达式 var 注册, 引用拼接）。
func (c *lispCompiler) andFn(args []lispExpr) (string, error) { return c.logical(" AND ", args) }
func (c *lispCompiler) orFn(args []lispExpr) (string, error)  { return c.logical(" OR ", args) }
func (c *lispCompiler) notFn(args []lispExpr) (string, error) { return c.logical("NOT ", args) }
func (c *lispCompiler) logical(sep string, args []lispExpr) (string, error) {
	parts := make([]string, 0, len(args))
	if sep == "NOT " {
		if len(args) != 1 {
			return "", fmt.Errorf("filter-lisp: not takes 1 arg")
		}
		frag, _, err := c.call(args[0])
		if err != nil {
			return "", err
		}
		return c.varRef("NOT (" + frag + ")"), nil
	}
	for _, a := range args {
		frag, _, err := c.call(a)
		if err != nil {
			return "", err
		}
		parts = append(parts, frag)
	}
	return c.varRef("(" + strings.Join(parts, sep) + ")"), nil
}

// edgeFn: (edge 字段) 存在性 / (edge 字段 目标) — 目标=值(折叠) 或 谓词(开层)。
// 方向由字段前缀决定: ->name 出边 / <-name 入边。
func (c *lispCompiler) edgeFn(args []lispExpr) (string, error) {
	if len(args) < 1 || len(args) > 2 {
		return "", fmt.Errorf("filter-lisp: edge takes 1 or 2 args")
	}
	path, err := pathOfC(args[0])
	if err != nil {
		return "", err
	}
	kind, refType, field := fieldOf(path)
	if kind != fieldOut && kind != fieldIn {
		return "", fmt.Errorf("filter-lisp: edge needs ref field with -> or <- prefix, got %q", path)
	}
	in := kind == fieldIn
	if in && refType == "" && field != "*" {
		return "", fmt.Errorf("filter-lisp: in-edge needs <-type.field (source type explicit), got %q", path)
	}
	// 通配入边 <-*: 任意来源; 一元=存在性, 二元=开层（谓词在 src 上,
	// 宽松编译 — 列通用, JSON 不校验 — 执行期才知道对错）
	if in && field == "*" {
		if len(args) == 1 {
			return c.varRef("EXISTS(SELECT 1 FROM edges WHERE to_node = " + c.link + ")"), nil
		}
		c.depth++
		alias := fmt.Sprintf("e%d", c.depth)
		srcAlias := fmt.Sprintf("t%d", c.depth)
		origRef, origLink := c.nodeRef, c.link
		c.nodeRef, c.link = srcAlias, srcAlias+".id"
		frag, _, err := c.call(args[1])
		c.nodeRef, c.link = origRef, origLink
		c.depth--
		if err != nil {
			return "", err
		}
		fragOut := fmt.Sprintf("EXISTS(SELECT 1 FROM edges %s JOIN nodes %s ON %s.id = %s.from_node WHERE %s.to_node = %s AND %s)",
			alias, srcAlias, srcAlias, alias, alias, c.link, frag)
		return c.varRef(fragOut), nil
	}
	joinCol, linkCol := "to_node", "from_node"
	if in {
		joinCol, linkCol = "from_node", "to_node"
	}

	// 一元: 存在性（入边带来源类型校验）
	if len(args) == 1 {
		if in {
			return c.varRef("EXISTS(SELECT 1 FROM edges JOIN nodes src ON src.id = edges.from_node WHERE edges.field = #{1} AND edges.to_node = "+c.link+" AND src.type = #{2})", field, refType), nil
		}
		return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND "+linkCol+" = "+c.link+")", field), nil
	}

	target := args[1]
	if target.head == "" {
		// 值 → 折叠（目标身份比较, 无 JOIN）; 数组 → 报错提示 in
		val, err := c.valueOf(target)
		if err != nil {
			return "", err
		}
		if _, isArr := val.([]any); isArr {
			return "", fmt.Errorf("filter-lisp: edge target is an array, use in for collections")
		}
		if in {
			return c.varRef("EXISTS(SELECT 1 FROM edges JOIN nodes src ON src.id = edges.from_node WHERE edges.field = #{1} AND edges.to_node = "+c.link+" AND edges.from_node = #{2} AND src.type = #{3})", field, val, refType), nil
		}
		return c.varRef("EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND "+linkCol+" = "+c.link+" AND "+joinCol+" = #{2})", field, val), nil
	}

	// 谓词 → 开层: 宿主别名切换到 t{i}（无类型校验 — 宽松; 入边带来源类型过滤）
	c.depth++
	alias := fmt.Sprintf("e%d", c.depth)
	tAlias := fmt.Sprintf("t%d", c.depth)
	origRef, origLink := c.nodeRef, c.link
	c.nodeRef, c.link = tAlias, tAlias+".id"
	frag, _, err := c.call(target)
	c.nodeRef, c.link = origRef, origLink
	c.depth--
	if err != nil {
		return "", err
	}
	if in {
		fragOut := fmt.Sprintf("EXISTS(SELECT 1 FROM edges %s JOIN nodes %s ON %s.id = %s.%s WHERE %s.field = #{1} AND %s.%s = %s AND %s.type = #{2} AND %s)",
			alias, tAlias, tAlias, alias, joinCol, alias, alias, linkCol, c.link, tAlias, frag)
		return c.varRef(fragOut, field, refType), nil
	}
	fragOut := fmt.Sprintf("EXISTS(SELECT 1 FROM edges %s JOIN nodes %s ON %s.id = %s.%s WHERE %s.field = #{1} AND %s.%s = %s AND %s)",
		alias, tAlias, tAlias, alias, joinCol, alias, alias, linkCol, c.link, frag)
	return c.varRef(fragOut, field), nil
}

// inFn: (in 字段 集合) — 字段=列/JSON(标量 IN) 或 引用(边 EXISTS, 方向看前缀)。
// 集合 = 数组字面量 [..] / 占位符数组 {:ids} / 集合函数 (subtree ..)。
func (c *lispCompiler) inFn(args []lispExpr) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("filter-lisp: in takes 2 args")
	}
	path, err := pathOfC(args[0])
	if err != nil {
		return "", err
	}
	kind, refType, field := fieldOf(path)
	if kind == fieldIn && refType == "" && field != "*" {
		return "", fmt.Errorf("filter-lisp: in-edge needs <-type.field (source type explicit), got %q", path)
	}

	// 集合形态一: 数组字面量 [1 2 3] 或占位符绑定数组（元素解析占位符）
	if arr, ok := c.arrayValue(args[1]); ok {
		return c.varRef(c.inSQL(kind, refType, field, arr), c.inArgs(kind, refType, field, arr)...), nil
	}
	// 集合形态二: 集合函数 (subtree "root") / 自定义 — 返回 id 切片参数
	if args[1].head != "" {
		setRef, setArgs, err := c.call(args[1])
		if err != nil {
			return "", err
		}
		if len(setArgs) > 0 {
			// 自定义函数直接参数（id 切片）— 拼 expand
			allArgs := append([]any{c.inFieldArg(kind, refType, field)}, setArgs...)
			return c.varRef(c.inSQLRef(kind, refType, field, "#{2|expand}"), allArgs...), nil
		}
		// subtree: ${eN} 引用（其 var 含 id 切片参数）— 直接引用
		if kind != fieldOut && kind != fieldIn {
			return "", fmt.Errorf("filter-lisp: subtree set is node ids, use with ref field (->name)")
		}
		return c.varRef(c.inSQLRef(kind, refType, field, setRef), c.inFieldArg(kind, refType, field)), nil
	}
	// 单值原子（非数组）: (in status 5) → 单元素集合
	val, err := c.valueOf(args[1])
	if err != nil {
		return "", err
	}
	arr := []any{val}
	return c.varRef(c.inSQL(kind, refType, field, arr), c.inArgs(kind, refType, field, arr)...), nil
}

// arrayValue 数组字面量 [..] / 占位符绑定数组 → ([]any, true); 否则 (nil, false)。
func (c *lispCompiler) arrayValue(e lispExpr) ([]any, bool) {
	if e.head != "" {
		return nil, false
	}
	switch v := e.atom.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			if p, ok := item.(placeholder); ok {
				val, err := valueParam(p, c.params)
				if err != nil {
					return nil, false
				}
				out[i] = val
			} else {
				out[i] = item
			}
		}
		return out, true
	case placeholder:
		val, err := valueParam(v, c.params)
		if err != nil {
			return nil, false
		}
		if arr, ok := val.([]any); ok {
			return arr, true
		}
		return nil, false
	}
	return nil, false
}

// inFieldArg 集合片段 #{1} 的参数（字段名 / JSON 路径）。
func (c *lispCompiler) inFieldArg(kind fieldKind, refType, field string) any {
	if kind == fieldJSON {
		return jsonPath(field)
	}
	return field
}

// inArgs 数组形态参数: [#{1} 字段, #{2|expand} 数组]（入边追加 #{3} 类型）。
func (c *lispCompiler) inArgs(kind fieldKind, refType, field string, arr []any) []any {
	if kind == fieldIn {
		if field == "*" {
			return []any{arr} // 通配: 只有集合（无 field/type 参数）
		}
		return []any{c.inFieldArg(kind, refType, field), arr, refType}
	}
	return []any{c.inFieldArg(kind, refType, field), arr}
}

// inSQL 标量/引用字段的 IN 片段（数组参数 → expand）。
func (c *lispCompiler) inSQL(kind fieldKind, refType, field string, arr []any) string {
	if len(arr) == 0 {
		return "1 = 0" // 空集合永假
	}
	switch kind {
	case fieldCol:
		return c.nodeRef + ".#{1|quote} IN (#{2|expand})"
	case fieldJSON:
		return "json_extract(" + c.nodeRef + ".fields, #{1}) IN (#{2|expand})"
	case fieldOut:
		return "EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND from_node = " + c.link + " AND to_node IN (#{2|expand}))"
	case fieldIn:
		if field == "*" {
			return "EXISTS(SELECT 1 FROM edges WHERE to_node = " + c.link + " AND from_node IN (#{1|expand}))"
		}
		return "EXISTS(SELECT 1 FROM edges JOIN nodes src ON src.id = edges.from_node WHERE edges.field = #{1} AND edges.to_node = " + c.link + " AND edges.from_node IN (#{2|expand}) AND src.type = #{3})"
	}
	return ""
}

// inSQLRef 集合函数形态（引用字段 — 集合是节点 id）; 标量字段拒绝对照。
func (c *lispCompiler) inSQLRef(kind fieldKind, refType, field, setRef string) string {
	if kind == fieldIn {
		return "EXISTS(SELECT 1 FROM edges JOIN nodes src ON src.id = edges.from_node WHERE edges.field = #{1} AND edges.to_node = " + c.link + " AND edges.from_node IN (" + setRef + ") AND src.type = #{2})"
	}
	joinCol, linkCol := "to_node", "from_node"
	return "EXISTS(SELECT 1 FROM edges WHERE field = #{1} AND " + linkCol + " = " + c.link + " AND " + joinCol + " IN (" + setRef + "))"
}

// subtreeFn: (subtree "slug") — 返回 id 列表（参数传给 in 的 expand）。
func (c *lispCompiler) subtreeFn(args []lispExpr) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("filter-lisp: subtree takes 1 arg")
	}
	slug, _ := pathOfC(args[0])
	cat, err := c.svc.GetNodeBySlug(slug)
	if err != nil || cat == nil {
		return "", fmt.Errorf("filter-lisp: subtree %q not found", slug)
	}
	ids, err := c.svc.Subtree(cat.Type, cat.ID, "parent", 20)
	if err != nil {
		return "", err
	}
	ids = append([]int64{cat.ID}, ids...)
	anyIDs := make([]any, len(ids))
	for i, id := range ids {
		anyIDs[i] = id
	}
	// 返回 ${eN} 引用（var 片段 = #{1|expand}, 参数 = id 切片 —
	// in 引用展开后是 expand 占位, 参数用 subtree 自己的）
	return c.varRef("#{1|expand}", anyIDs), nil
}
