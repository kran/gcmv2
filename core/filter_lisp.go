// Lisp filter: S-expression 语法 — 函数即原语, 注册驱动。
//
//	语法:  (函数 参数...)
//	内置:  = != > >= < <= like and or not edge in subtree
//	站点:  RegisterLispFuncC(name, fn) — 自定义函数（任意 AST → SQL 片段,
//	       可组合/嵌套 — 图原语、集合、业务查询都进表达式层）。
//
//	取值:  created_at(列) / $name(JSON) / ->field(出边) / <-type.field(入边)
//	引用:  (edge 字段 目标) — 一元=存在性; 值=折叠; 谓词=开层
//	集合:  (in 字段 集合) — 数组字面量 [1 2 3] / 占位符 / (subtree "address")
//
//	值: 数字 / "字符串" / true / false / {:占位符}
package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ── AST ──────────────────────────────────────────

// lispExpr 表达式: 原子（值）或列表（函数调用）。
type lispExpr struct {
	atom any    // 原子值（string/float64/bool/placeholder）; 列表时 nil
	head string // 函数名（列表时）
	args []lispExpr
}

// ── Parser（括号递归, ~60 行）────────────────────

const (
	maxFilterBytes = 4096
	maxFilterDepth = 12
	maxFilterNodes = 256
	maxArrayItems  = 100
)

type lispParser struct {
	src   string
	pos   int
	nodes int
}

// parseLisp 解析 S-expression → 表达式树，并限制输入和 AST 复杂度。
func parseLisp(src string) (lispExpr, error) {
	if len(src) > maxFilterBytes {
		return lispExpr{}, fmt.Errorf("filter-lisp: expression exceeds %d bytes", maxFilterBytes)
	}
	p := &lispParser{src: src}
	p.skipWS()
	e, err := p.parseExpr(0)
	if err != nil {
		return lispExpr{}, err
	}
	p.skipWS()
	if p.pos < len(p.src) {
		return lispExpr{}, fmt.Errorf("filter-lisp: unexpected trailing %q", p.src[p.pos:])
	}
	return e, nil
}

func (p *lispParser) parseExpr(depth int) (lispExpr, error) {
	if depth > maxFilterDepth {
		return lispExpr{}, fmt.Errorf("filter-lisp: nesting exceeds %d", maxFilterDepth)
	}
	p.nodes++
	if p.nodes > maxFilterNodes {
		return lispExpr{}, fmt.Errorf("filter-lisp: expression exceeds %d nodes", maxFilterNodes)
	}
	p.skipWS()
	if p.pos >= len(p.src) {
		return lispExpr{}, fmt.Errorf("filter-lisp: unexpected end")
	}
	c := p.src[p.pos]
	switch {
	case c == '(':
		p.pos++ // (
		p.skipWS()
		name, err := p.parseToken()
		if err != nil {
			return lispExpr{}, err
		}
		e := lispExpr{head: name}
		for {
			p.skipWS()
			if p.pos >= len(p.src) {
				return lispExpr{}, fmt.Errorf("filter-lisp: unterminated (")
			}
			if p.src[p.pos] == ')' {
				p.pos++
				break
			}
			arg, err := p.parseExpr(depth + 1)
			if err != nil {
				return lispExpr{}, err
			}
			e.args = append(e.args, arg)
		}
		return e, nil
	case c == ')':
		return lispExpr{}, fmt.Errorf("filter-lisp: unexpected )")
	case c == '[':
		// 数组字面量 [1 2 3] / ["a" "b"] — 元素为原子（值/占位符）
		p.pos++
		var items []any
		for {
			p.skipWS()
			if p.pos >= len(p.src) {
				return lispExpr{}, fmt.Errorf("filter-lisp: unterminated [")
			}
			if p.src[p.pos] == ']' {
				p.pos++
				break
			}
			if len(items) >= maxArrayItems {
				return lispExpr{}, fmt.Errorf("filter-lisp: array exceeds %d items", maxArrayItems)
			}
			item, err := p.parseExpr(depth + 1)
			if err != nil {
				return lispExpr{}, err
			}
			if item.head != "" {
				return lispExpr{}, fmt.Errorf("filter-lisp: array element must be a value, got (%s ...)", item.head)
			}
			items = append(items, item.atom)
		}
		return lispExpr{atom: items}, nil
	default:
		tok, err := p.parseToken()
		if err != nil {
			return lispExpr{}, err
		}
		return atomOf(tok), nil
	}
}

func (p *lispParser) parseToken() (string, error) {
	p.skipWS()
	start := p.pos
	if p.pos < len(p.src) && p.src[p.pos] == '"' {
		// 引号字符串: 整段读到配对引号（含空格/括号）; \" 转义
		p.pos++
		escaped := false
		for p.pos < len(p.src) {
			c := p.src[p.pos]
			if escaped {
				escaped = false
				p.pos++
				continue
			}
			if c == '\\' {
				escaped = true
				p.pos++
				continue
			}
			if c == '"' {
				break
			}
			p.pos++
		}
		if p.pos >= len(p.src) {
			return "", fmt.Errorf("filter-lisp: unterminated string at %d", start)
		}
		p.pos++ // 闭引号
		return p.src[start:p.pos], nil
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ')' || c == '[' || c == ']' {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", fmt.Errorf("filter-lisp: empty token at %d", p.pos)
	}
	return p.src[start:p.pos], nil
}

func (p *lispParser) skipWS() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t' || p.src[p.pos] == '\n') {
		p.pos++
	}
}

func atomOf(tok string) lispExpr {
	if len(tok) >= 2 && tok[0] == '"' && tok[len(tok)-1] == '"' {
		return lispExpr{atom: unescapeStr(tok[1 : len(tok)-1])}
	}
	if strings.HasPrefix(tok, "{:") && strings.HasSuffix(tok, "}") {
		return lispExpr{atom: placeholder{name: tok[2 : len(tok)-1]}}
	}
	if n, err := strconv.ParseFloat(tok, 64); err == nil {
		return lispExpr{atom: n}
	}
	if tok == "true" {
		return lispExpr{atom: true}
	}
	if tok == "false" {
		return lispExpr{atom: false}
	}
	return lispExpr{atom: tok}
}

// ── 占位符（{:name}）──────────────────────────────

// placeholder 占位符值（编译时经 params 绑定）。
type placeholder struct{ name string }

// valueParam 值/占位符/数组 → 参数值（数组元素逐个解析占位符）。
func valueParam(v any, params map[string]any) (any, error) {
	switch t := v.(type) {
	case placeholder:
		val, ok := params[t.name]
		if !ok {
			return nil, fmt.Errorf("filter-lisp: placeholder {: %s} not bound", t.name)
		}
		return val, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			if p, ok := item.(placeholder); ok {
				val, ok := params[p.name]
				if !ok {
					return nil, fmt.Errorf("filter-lisp: placeholder {: %s} not bound", p.name)
				}
				out[i] = val
			} else {
				out[i] = item
			}
		}
		return out, nil
	}
	return v, nil
}

func unescapeStr(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	escaped := false
	for _, c := range s {
		if escaped {
			b.WriteRune(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(c)
	}
	if escaped {
		b.WriteByte('\\')
	}
	return b.String()
}
