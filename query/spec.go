package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Spec is the transport representation for trusted generic query clients.
type Spec struct {
	Where *PredicateSpec `json:"where,omitempty"`
	Sort  []SortSpec     `json:"sort,omitempty"`
	Page  Page           `json:"page"`
}

type SortSpec struct {
	Field  string `json:"field,omitempty"`
	Column string `json:"column,omitempty"`
	Desc   bool   `json:"desc,omitempty"`
}

type IncomingSpec struct {
	Type  string `json:"type"`
	Field string `json:"field"`
}

type PredicateSpec struct {
	Op       string          `json:"op"`
	Field    string          `json:"field,omitempty"`
	Column   string          `json:"column,omitempty"`
	Ref      string          `json:"ref,omitempty"`
	Incoming *IncomingSpec   `json:"incoming,omitempty"`
	Value    any             `json:"value,omitempty"`
	Values   []any           `json:"values,omitempty"`
	Args     []PredicateSpec `json:"args,omitempty"`
	Where    *PredicateSpec  `json:"where,omitempty"`
	Subtree  string          `json:"subtree,omitempty"`
}

const MaxSpecBytes = 16 * 1024

// DecodeSpec decodes one strict JSON value and converts it to typed query values.
func DecodeSpec(reader io.Reader) (Expr, []SortField, Page, error) {
	body, err := io.ReadAll(io.LimitReader(reader, MaxSpecBytes+1))
	if err != nil {
		return nil, nil, Page{}, fmt.Errorf("query: spec: %w", err)
	}
	if len(body) > MaxSpecBytes {
		return nil, nil, Page{}, fmt.Errorf("query: spec exceeds %d bytes", MaxSpecBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var spec Spec
	if err := decoder.Decode(&spec); err != nil {
		return nil, nil, Page{}, fmt.Errorf("query: spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, nil, Page{}, fmt.Errorf("query: spec must contain one JSON value")
		}
		return nil, nil, Page{}, fmt.Errorf("query: spec: %w", err)
	}
	var where Expr
	if spec.Where != nil {
		nodes := 0
		where, err = spec.Where.expr(0, &nodes)
		if err != nil {
			return nil, nil, Page{}, err
		}
	}
	sortFields := make([]SortField, len(spec.Sort))
	for i, item := range spec.Sort {
		if (item.Field == "") == (item.Column == "") {
			return nil, nil, Page{}, fmt.Errorf("query: spec sort[%d] requires exactly one of field or column", i)
		}
		path := Field(item.Field)
		if item.Column != "" {
			path = System(item.Column)
		}
		sortFields[i] = SortField{Path: path, Desc: item.Desc}
	}
	return where, sortFields, spec.Page, nil
}

func (spec PredicateSpec) expr(depth int, nodes *int) (Expr, error) {
	if depth > MaxFilterDepth {
		return nil, fmt.Errorf("query: spec nesting exceeds %d", MaxFilterDepth)
	}
	*nodes = *nodes + 1
	if *nodes > MaxFilterNodes {
		return nil, fmt.Errorf("query: spec exceeds %d nodes", MaxFilterNodes)
	}
	path, err := spec.path()
	if err != nil && spec.Op != "and" && spec.Op != "or" && spec.Op != "not" && spec.Op != "true" && spec.Op != "false" {
		return nil, err
	}
	switch spec.Op {
	case "eq", "ne", "gt", "gte", "lt", "lte", "contains", "prefix":
		ops := map[string]CompareOp{
			"eq": OpEQ, "ne": OpNE, "gt": OpGT, "gte": OpGTE,
			"lt": OpLT, "lte": OpLTE, "contains": OpContains, "prefix": OpPrefix,
		}
		return Compare{Op: ops[spec.Op], Path: path, Value: spec.Value}, nil
	case "in":
		if spec.Subtree != "" {
			return In{Path: path, Set: Subtree{Address: spec.Subtree}}, nil
		}
		return In{Path: path, Values: append([]any(nil), spec.Values...)}, nil
	case "exists", "missing":
		return Exists{Path: path, Missing: spec.Op == "missing"}, nil
	case "related":
		if spec.Where == nil {
			return nil, fmt.Errorf("query: spec related.where required")
		}
		where, err := spec.Where.expr(depth+1, nodes)
		if err != nil {
			return nil, err
		}
		return Related{Path: path, Where: where}, nil
	case "and", "or":
		if len(spec.Args) == 0 {
			return nil, fmt.Errorf("query: spec %s.args required", spec.Op)
		}
		args := make([]Expr, len(spec.Args))
		for i := range spec.Args {
			args[i], err = spec.Args[i].expr(depth+1, nodes)
			if err != nil {
				return nil, err
			}
		}
		if spec.Op == "and" {
			return And(args...), nil
		}
		return Or(args...), nil
	case "not":
		if len(spec.Args) != 1 {
			return nil, fmt.Errorf("query: spec not requires exactly one arg")
		}
		arg, err := spec.Args[0].expr(depth+1, nodes)
		if err != nil {
			return nil, err
		}
		return Not(arg), nil
	case "true":
		return True(), nil
	case "false":
		return False(), nil
	default:
		return nil, fmt.Errorf("query: spec unknown op %q", spec.Op)
	}
}

func (spec PredicateSpec) path() (Path, error) {
	set := 0
	if spec.Field != "" {
		set++
	}
	if spec.Column != "" {
		set++
	}
	if spec.Ref != "" {
		set++
	}
	if spec.Incoming != nil {
		set++
	}
	if set != 1 {
		return Path{}, fmt.Errorf("query: spec requires exactly one of field, column, ref, or incoming")
	}
	if spec.Ref != "" {
		return Ref(spec.Ref), nil
	}
	if spec.Incoming != nil {
		if spec.Incoming.Type == "" || spec.Incoming.Field == "" {
			return Path{}, fmt.Errorf("query: spec incoming type and field required")
		}
		return Incoming(spec.Incoming.Type, spec.Incoming.Field), nil
	}
	if spec.Column != "" {
		return System(spec.Column), nil
	}
	return Field(spec.Field), nil
}
