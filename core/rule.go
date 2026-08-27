package core

import (
	"fmt"
	"strconv"
	"strings"
)

// ── 权限规则求值器（纯内存 Lisp 子集） ──
//
// 用途: types.yaml 的 create 规则（如 '(= auth.role "editor")'）— 不生成
// SQL（filter 编译器是 SQL 的）— 在内存上下文上求值。
//
// 语法子集:
//
//	and / or / not             逻辑
//	= / !=                     比较（值、符号路径、nil）
//	in                         (in x [a b c])
//	字面量: 字符串 / 数字 / true / false / nil
//	符号路径: auth.role → ctx["auth"]["role"]（多层 . 穿透; 缺键 = nil）
//
// 求值约定: 值真假 — nil/0/""/false = 假; 其余 = 真。

// EvalRule 对 ctx 求值规则表达式; 语法错误返回 error（fail-loud）。
func EvalRule(expr string, ctx map[string]any) (bool, error) {
	toks, err := tokenizeRule(expr)
	if err != nil {
		return false, err
	}
	p := &ruleParser{toks: toks, ctx: ctx}
	v, err := p.parseExpr()
	if err != nil {
		return false, err
	}
	if p.pos != len(p.toks) {
		return false, fmt.Errorf("rule: trailing tokens at %q", p.toks[p.pos])
	}
	return truthy(v), nil
}

// truthy 值真假（nil/0/""/false = 假）。
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// ── 词法 ─────────────────────────────────────

type ruleTok struct {
	kind string // "(" ")" "str" "num" "sym"
	val  string
}

func tokenizeRule(expr string) ([]ruleTok, error) {
	var toks []ruleTok
	i := 0
	for i < len(expr) {
		c := expr[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			toks = append(toks, ruleTok{kind: "("})
			i++
		case c == ')':
			toks = append(toks, ruleTok{kind: ")"})
			i++
		case c == '[':
			toks = append(toks, ruleTok{kind: "["})
			i++
		case c == ']':
			toks = append(toks, ruleTok{kind: "]"})
			i++
		case c == '"':
			j := i + 1
			var sb strings.Builder
			for j < len(expr) {
				if expr[j] == '\\' && j+1 < len(expr) {
					sb.WriteByte(expr[j+1])
					j += 2
					continue
				}
				if expr[j] == '"' {
					break
				}
				sb.WriteByte(expr[j])
				j++
			}
			if j >= len(expr) {
				return nil, fmt.Errorf("rule: unterminated string at %d", i)
			}
			toks = append(toks, ruleTok{kind: "str", val: sb.String()})
			i = j + 1
		default:
			j := i
			for j < len(expr) && expr[j] != ' ' && expr[j] != '\t' && expr[j] != '\n' &&
				expr[j] != '\r' && expr[j] != '(' && expr[j] != ')' &&
				expr[j] != '[' && expr[j] != ']' {
				j++
			}
			toks = append(toks, ruleTok{kind: "sym", val: expr[i:j]})
			i = j
		}
	}
	return toks, nil
}

// ── 解析/求值（一体 — 表达式即程序） ─────────────

type ruleParser struct {
	toks []ruleTok
	pos  int
	ctx  map[string]any
}

func (p *ruleParser) peek() *ruleTok {
	if p.pos >= len(p.toks) {
		return nil
	}
	return &p.toks[p.pos]
}

func (p *ruleParser) next() *ruleTok {
	t := p.peek()
	if t != nil {
		p.pos++
	}
	return t
}

// parseExpr 求值一个表达式: 字面量 / 符号 / (op args...)
func (p *ruleParser) parseExpr() (any, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("rule: unexpected end")
	}
	if t.kind == "[" {
		p.next()
		var list []any
		for {
			if p.peek() == nil {
				return nil, fmt.Errorf("rule: unterminated list")
			}
			if p.peek().kind == "]" {
				p.next()
				break
			}
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		return list, nil
	}
	if t.kind == "(" {
		p.next()
		op := p.next()
		if op == nil || op.kind != "sym" {
			return nil, fmt.Errorf("rule: expected operator")
		}
		var args []any
		for {
			if p.peek() == nil {
				return nil, fmt.Errorf("rule: unterminated list")
			}
			if p.peek().kind == ")" {
				p.next()
				break
			}
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, v)
		}
		return p.apply(op.val, args)
	}
	p.next()
	switch t.kind {
	case "str":
		return t.val, nil
	case "num":
		f, err := strconv.ParseFloat(t.val, 64)
		if err != nil {
			return nil, fmt.Errorf("rule: bad number %q", t.val)
		}
		return f, nil
	case "sym":
		// 数字字面量（42 / 3.14 — 先于符号解析）
		if f, err := strconv.ParseFloat(t.val, 64); err == nil {
			return f, nil
		}
		// 顶层中缀: auth != nil / auth.role = "editor"（= 与 != 两元）
		if op := p.peek(); op != nil && (op.val == "=" || op.val == "!=") {
			p.next()
			rhs, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			lhs, err := p.lookup(t.val)
			if err != nil {
				return nil, err
			}
			return p.apply(op.val, []any{lhs, rhs})
		}
		return p.lookup(t.val)
	}
	return nil, fmt.Errorf("rule: unexpected token %q", t.kind)
}

// lookup 符号解析: nil / true / false / 路径（a.b.c 从 ctx 穿透）。
func (p *ruleParser) lookup(sym string) (any, error) {
	switch sym {
	case "nil":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	var cur any = p.ctx
	for _, part := range strings.Split(sym, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, nil // 路径中断 = nil
		}
		cur, ok = m[part]
		if !ok {
			return nil, nil
		}
	}
	return cur, nil
}

// apply 操作符求值。
func (p *ruleParser) apply(op string, args []any) (any, error) {
	switch op {
	case "and":
		for _, a := range args {
			if !truthy(a) {
				return false, nil
			}
		}
		return true, nil
	case "or":
		for _, a := range args {
			if truthy(a) {
				return true, nil
			}
		}
		return false, nil
	case "not":
		if len(args) != 1 {
			return nil, fmt.Errorf("rule: not takes 1 arg")
		}
		return !truthy(args[0]), nil
	case "=", "!=":
		if len(args) != 2 {
			return nil, fmt.Errorf("rule: %s takes 2 args", op)
		}
		eq := equalRule(args[0], args[1])
		if op == "!=" {
			return !eq, nil
		}
		return eq, nil
	case "in":
		if len(args) != 2 {
			return nil, fmt.Errorf("rule: in takes 2 args")
		}
		list, ok := args[1].([]any)
		if !ok {
			return nil, fmt.Errorf("rule: in expects list")
		}
		for _, e := range list {
			if equalRule(args[0], e) {
				return true, nil
			}
		}
		return false, nil
	}
	return nil, fmt.Errorf("rule: unknown operator %q", op)
}

// equalRule 宽松相等（字符串 vs 数字 vs nil — 字段值类型不定）。
func equalRule(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	af, aIsNum := toFloat(a)
	bf, bIsNum := toFloat(b)
	if aIsNum && bIsNum {
		return af == bf
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}
