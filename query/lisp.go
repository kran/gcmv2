package query

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	MaxFilterBytes = 4096
	MaxFilterDepth = 12
	MaxFilterNodes = 256
	MaxSetValues   = 100
)

type sexpr struct {
	atom any
	head string
	args []sexpr
}

type placeholder struct{ name string }

type lispParser struct {
	src   string
	pos   int
	nodes int
}

// ParseLisp parses the trusted textual query syntax into the same AST used by
// the Go builder. Placeholders are resolved before the AST is returned.
func ParseLisp(src string, params map[string]any) (Expr, error) {
	if len(src) > MaxFilterBytes {
		return nil, fmt.Errorf("query: lisp expression exceeds %d bytes", MaxFilterBytes)
	}
	parser := &lispParser{src: src}
	parser.skipSpace()
	raw, err := parser.parseExpr(0)
	if err != nil {
		return nil, err
	}
	parser.skipSpace()
	if parser.pos != len(parser.src) {
		return nil, fmt.Errorf("query: lisp unexpected trailing input at %d", parser.pos)
	}
	return buildExpr(raw, params)
}

func (p *lispParser) parseExpr(depth int) (sexpr, error) {
	if depth > MaxFilterDepth {
		return sexpr{}, fmt.Errorf("query: lisp nesting exceeds %d", MaxFilterDepth)
	}
	p.nodes++
	if p.nodes > MaxFilterNodes {
		return sexpr{}, fmt.Errorf("query: lisp expression exceeds %d nodes", MaxFilterNodes)
	}
	p.skipSpace()
	if p.pos >= len(p.src) {
		return sexpr{}, fmt.Errorf("query: lisp unexpected end")
	}
	switch p.src[p.pos] {
	case '(':
		p.pos++
		p.skipSpace()
		head, err := p.parseToken()
		if err != nil {
			return sexpr{}, err
		}
		out := sexpr{head: head}
		for {
			p.skipSpace()
			if p.pos >= len(p.src) {
				return sexpr{}, fmt.Errorf("query: lisp unterminated (")
			}
			if p.src[p.pos] == ')' {
				p.pos++
				return out, nil
			}
			arg, err := p.parseExpr(depth + 1)
			if err != nil {
				return sexpr{}, err
			}
			out.args = append(out.args, arg)
		}
	case ')':
		return sexpr{}, fmt.Errorf("query: lisp unexpected )")
	case '[':
		p.pos++
		items := make([]any, 0)
		for {
			p.skipSpace()
			if p.pos >= len(p.src) {
				return sexpr{}, fmt.Errorf("query: lisp unterminated [")
			}
			if p.src[p.pos] == ']' {
				p.pos++
				return sexpr{atom: items}, nil
			}
			if len(items) >= MaxSetValues {
				return sexpr{}, fmt.Errorf("query: lisp array exceeds %d items", MaxSetValues)
			}
			item, err := p.parseExpr(depth + 1)
			if err != nil {
				return sexpr{}, err
			}
			if item.head != "" {
				return sexpr{}, fmt.Errorf("query: lisp array elements must be values")
			}
			items = append(items, item.atom)
		}
	default:
		token, err := p.parseToken()
		if err != nil {
			return sexpr{}, err
		}
		return parseAtom(token), nil
	}
}

func (p *lispParser) parseToken() (string, error) {
	p.skipSpace()
	start := p.pos
	if p.pos < len(p.src) && p.src[p.pos] == '"' {
		p.pos++
		escaped := false
		for p.pos < len(p.src) {
			char := p.src[p.pos]
			if escaped {
				escaped = false
				p.pos++
				continue
			}
			if char == '\\' {
				escaped = true
				p.pos++
				continue
			}
			if char == '"' {
				p.pos++
				return p.src[start:p.pos], nil
			}
			p.pos++
		}
		return "", fmt.Errorf("query: lisp unterminated string at %d", start)
	}
	for p.pos < len(p.src) {
		char := p.src[p.pos]
		if strings.ContainsRune(" \t\n()[]", rune(char)) {
			break
		}
		p.pos++
	}
	if start == p.pos {
		return "", fmt.Errorf("query: lisp empty token at %d", p.pos)
	}
	return p.src[start:p.pos], nil
}

func (p *lispParser) skipSpace() {
	for p.pos < len(p.src) && strings.ContainsRune(" \t\n", rune(p.src[p.pos])) {
		p.pos++
	}
}

func parseAtom(token string) sexpr {
	if len(token) >= 2 && token[0] == '"' && token[len(token)-1] == '"' {
		value, err := strconv.Unquote(token)
		if err == nil {
			return sexpr{atom: value}
		}
	}
	if strings.HasPrefix(token, "{:") && strings.HasSuffix(token, "}") {
		return sexpr{atom: placeholder{name: token[2 : len(token)-1]}}
	}
	if number, err := strconv.ParseFloat(token, 64); err == nil {
		return sexpr{atom: number}
	}
	if token == "true" {
		return sexpr{atom: true}
	}
	if token == "false" {
		return sexpr{atom: false}
	}
	if token == "null" {
		return sexpr{atom: nil}
	}
	return sexpr{atom: token}
}

func buildExpr(raw sexpr, params map[string]any) (Expr, error) {
	if raw.head == "" {
		return nil, fmt.Errorf("query: lisp top-level must be a call")
	}
	switch raw.head {
	case "=", "!=", ">", ">=", "<", "<=", "contains", "prefix":
		if len(raw.args) != 2 {
			return nil, fmt.Errorf("query: lisp %s takes 2 arguments", raw.head)
		}
		path, err := parsePath(raw.args[0])
		if err != nil {
			return nil, err
		}
		value, err := resolveValue(raw.args[1], params)
		if err != nil {
			return nil, err
		}
		ops := map[string]CompareOp{
			"=": OpEQ, "!=": OpNE, ">": OpGT, ">=": OpGTE,
			"<": OpLT, "<=": OpLTE, "contains": OpContains, "prefix": OpPrefix,
		}
		return Compare{Op: ops[raw.head], Path: path, Value: value}, nil
	case "and", "or":
		if len(raw.args) == 0 {
			return nil, fmt.Errorf("query: lisp %s requires arguments", raw.head)
		}
		args := make([]Expr, len(raw.args))
		for i := range raw.args {
			expression, err := buildExpr(raw.args[i], params)
			if err != nil {
				return nil, err
			}
			args[i] = expression
		}
		op := OpAnd
		if raw.head == "or" {
			op = OpOr
		}
		return Logic{Op: op, Args: args}, nil
	case "not":
		if len(raw.args) != 1 {
			return nil, fmt.Errorf("query: lisp not takes 1 argument")
		}
		expression, err := buildExpr(raw.args[0], params)
		if err != nil {
			return nil, err
		}
		return Not(expression), nil
	case "in":
		if len(raw.args) != 2 {
			return nil, fmt.Errorf("query: lisp in takes 2 arguments")
		}
		path, err := parsePath(raw.args[0])
		if err != nil {
			return nil, err
		}
		if raw.args[1].head == "subtree" {
			set, err := buildSet(raw.args[1], params)
			if err != nil {
				return nil, err
			}
			return In{Path: path, Set: set}, nil
		}
		value, err := resolveValue(raw.args[1], params)
		if err != nil {
			return nil, err
		}
		values, err := sliceValues(value)
		if err != nil {
			return nil, err
		}
		return In{Path: path, Values: values}, nil
	case "exists", "missing":
		if len(raw.args) != 1 {
			return nil, fmt.Errorf("query: lisp %s takes 1 argument", raw.head)
		}
		path, err := parsePath(raw.args[0])
		if err != nil {
			return nil, err
		}
		return Exists{Path: path, Missing: raw.head == "missing"}, nil
	case "related":
		if len(raw.args) != 2 {
			return nil, fmt.Errorf("query: lisp related takes 2 arguments")
		}
		path, err := parsePath(raw.args[0])
		if err != nil {
			return nil, err
		}
		where, err := buildExpr(raw.args[1], params)
		if err != nil {
			return nil, err
		}
		return Related{Path: path, Where: where}, nil
	default:
		return nil, fmt.Errorf("query: lisp unknown function %q", raw.head)
	}
}

func buildSet(raw sexpr, params map[string]any) (Set, error) {
	if raw.head != "subtree" || len(raw.args) != 1 {
		return nil, fmt.Errorf("query: lisp invalid set expression %q", raw.head)
	}
	value, err := resolveValue(raw.args[0], params)
	if err != nil {
		return nil, err
	}
	address, ok := value.(string)
	if !ok || address == "" {
		return nil, fmt.Errorf("query: lisp subtree address must be a string")
	}
	return Subtree{Address: address}, nil
}

func parsePath(raw sexpr) (Path, error) {
	if raw.head != "" {
		return Path{}, fmt.Errorf("query: lisp path must be a value")
	}
	value, ok := raw.atom.(string)
	if !ok || value == "" {
		return Path{}, fmt.Errorf("query: lisp path must be a string")
	}
	switch {
	case strings.HasPrefix(value, "->"):
		return Ref(strings.TrimPrefix(value, "->")), nil
	case strings.HasPrefix(value, "<-"):
		parts := strings.Split(strings.TrimPrefix(value, "<-"), ".")
		if len(parts) != 2 {
			return Path{}, fmt.Errorf("query: incoming path must be <-type.field")
		}
		return Incoming(parts[0], parts[1]), nil
	case strings.HasPrefix(value, "$"):
		name := strings.TrimPrefix(value, "$")
		if name == "" || strings.HasPrefix(name, ".") {
			return Path{}, fmt.Errorf("query: dynamic path must use $field")
		}
		return Field(name), nil
	default:
		return System(value), nil
	}
}

func resolveValue(raw sexpr, params map[string]any) (any, error) {
	if raw.head != "" {
		return nil, fmt.Errorf("query: lisp expected value, got (%s ...)", raw.head)
	}
	return resolveAtom(raw.atom, params)
}

func resolveAtom(value any, params map[string]any) (any, error) {
	switch item := value.(type) {
	case placeholder:
		resolved, ok := params[item.name]
		if !ok {
			return nil, fmt.Errorf("query: lisp placeholder {:%s} not bound", item.name)
		}
		return resolved, nil
	case []any:
		out := make([]any, len(item))
		for i := range item {
			resolved, err := resolveAtom(item[i], params)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return value, nil
	}
}

func sliceValues(value any) ([]any, error) {
	if value == nil {
		return nil, fmt.Errorf("query: in values must be an array")
	}
	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return []any{value}, nil
	}
	if v.Len() > MaxSetValues {
		return nil, fmt.Errorf("query: collection exceeds %d items", MaxSetValues)
	}
	out := make([]any, v.Len())
	for i := range v.Len() {
		out[i] = v.Index(i).Interface()
	}
	return out, nil
}
