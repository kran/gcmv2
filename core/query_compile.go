package core

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kran/dba"
	gquery "github.com/kran/gcmv2/query"
	"github.com/kran/gcmv2/types"
)

var (
	ErrInvalidQuery    = errors.New("core: invalid query")
	ErrInvalidField    = errors.New("core: invalid query field")
	ErrInvalidOperator = errors.New("core: invalid query operator")
	ErrInvalidValue    = errors.New("core: invalid query value")
	ErrQueryTooComplex = errors.New("core: query too complex")
)

const maxRelationDepth = 4

type queryCompiler struct {
	service *Service
	nodes   int
	alias   int
}

func (s *Service) compileWhere(typeName string, expression gquery.Expr) (dba.Node, error) {
	return s.compileWhereAt(typeName, expression, "nodes")
}

func (s *Service) compileWhereAt(typeName string, expression gquery.Expr, nodeRef string) (dba.Node, error) {
	if _, ok := s.types.Type(typeName); !ok {
		return dba.Node{}, fmt.Errorf("%w: type %q not defined", ErrInvalidQuery, typeName)
	}
	if expression == nil {
		return dba.Expr("1 = 1"), nil
	}
	compiler := &queryCompiler{service: s}
	return compiler.compile(expression, typeName, nodeRef, 0)
}

func (c *queryCompiler) compile(expression gquery.Expr, typeName, nodeRef string, depth int) (dba.Node, error) {
	c.nodes++
	if c.nodes > gquery.MaxFilterNodes || depth > gquery.MaxFilterDepth {
		return dba.Node{}, ErrQueryTooComplex
	}
	switch expr := expression.(type) {
	case gquery.Constant:
		if bool(expr) {
			return dba.Expr("1 = 1"), nil
		}
		return dba.Expr("1 = 0"), nil
	case gquery.Compare:
		return c.compileCompare(expr, typeName, nodeRef)
	case gquery.Logic:
		return c.compileLogic(expr, typeName, nodeRef, depth)
	case gquery.In:
		return c.compileIn(expr, typeName, nodeRef)
	case gquery.Exists:
		return c.compileExists(expr, typeName, nodeRef)
	case gquery.Related:
		return c.compileRelated(expr, typeName, nodeRef, depth)
	default:
		return dba.Node{}, fmt.Errorf("%w: unsupported expression %T", ErrInvalidQuery, expression)
	}
}

type resolvedPath struct {
	sql       string
	field     types.FieldDef
	hasField  bool
	target    string
	fieldName string
}

func (c *queryCompiler) resolvePath(path gquery.Path, typeName, nodeRef string) (resolvedPath, error) {
	if path.Field == "" {
		return resolvedPath{}, fmt.Errorf("%w: empty path", ErrInvalidField)
	}
	switch path.Kind {
	case gquery.PathSystem:
		if !types.IsNodeColumn(path.Field) || path.Field == "fields" {
			return resolvedPath{}, fmt.Errorf("%w: system field %q", ErrInvalidField, path.Field)
		}
		return resolvedPath{sql: nodeRef + "." + quoteIdentifier(path.Field), fieldName: path.Field}, nil
	case gquery.PathField:
		field, ok := c.service.types.Field(typeName, path.Field)
		if !ok || c.service.types.IsRefKind(field.Kind) {
			return resolvedPath{}, fmt.Errorf("%w: %s.%s", ErrInvalidField, typeName, path.Field)
		}
		return resolvedPath{
			sql:       `json_extract(` + nodeRef + `.fields, '$.` + field.Name + `')`,
			field:     field,
			hasField:  true,
			fieldName: field.Name,
		}, nil
	case gquery.PathOutRef:
		field, ok := c.service.types.Field(typeName, path.Field)
		if !ok || !c.service.types.IsRefKind(field.Kind) {
			return resolvedPath{}, fmt.Errorf("%w: %s.%s is not a ref", ErrInvalidField, typeName, path.Field)
		}
		return resolvedPath{field: field, hasField: true, target: field.To, fieldName: field.Name}, nil
	case gquery.PathInRef:
		if path.SourceType == "" {
			return resolvedPath{}, fmt.Errorf("%w: incoming source type required", ErrInvalidField)
		}
		field, ok := c.service.types.Field(path.SourceType, path.Field)
		if !ok || !c.service.types.IsRefKind(field.Kind) || field.To != typeName {
			return resolvedPath{}, fmt.Errorf("%w: incoming %s.%s does not target %s", ErrInvalidField, path.SourceType, path.Field, typeName)
		}
		return resolvedPath{field: field, hasField: true, target: path.SourceType, fieldName: field.Name}, nil
	default:
		return resolvedPath{}, fmt.Errorf("%w: unknown path kind", ErrInvalidField)
	}
}

func (c *queryCompiler) compileCompare(expr gquery.Compare, typeName, nodeRef string) (dba.Node, error) {
	path, err := c.resolvePath(expr.Path, typeName, nodeRef)
	if err != nil {
		return dba.Node{}, err
	}
	if expr.Path.Kind == gquery.PathOutRef || expr.Path.Kind == gquery.PathInRef {
		return dba.Node{}, fmt.Errorf("%w: relation %q requires in/exists/related", ErrInvalidOperator, expr.Path.Field)
	}
	if err = c.validateCompare(typeName, expr.Op, path, expr.Value); err != nil {
		return dba.Node{}, err
	}
	if expr.Value == nil {
		if expr.Op == gquery.OpEQ {
			return dba.Expr(path.sql + " IS NULL"), nil
		}
		return dba.Expr(path.sql + " IS NOT NULL"), nil
	}
	operators := map[gquery.CompareOp]string{
		gquery.OpEQ: "=", gquery.OpNE: "!=", gquery.OpGT: ">", gquery.OpGTE: ">=",
		gquery.OpLT: "<", gquery.OpLTE: "<=",
	}
	if expr.Op == gquery.OpContains || expr.Op == gquery.OpPrefix {
		value := escapeLike(expr.Value.(string))
		if expr.Op == gquery.OpContains {
			value = "%" + value + "%"
		} else {
			value += "%"
		}
		return dba.Expr(path.sql+` LIKE #{1} ESCAPE '\'`, value), nil
	}
	return dba.Expr(path.sql+" "+operators[expr.Op]+" #{1}", expr.Value), nil
}

func (c *queryCompiler) validateCompare(typeName string, op gquery.CompareOp, path resolvedPath, value any) error {
	validOp := op == gquery.OpEQ || op == gquery.OpNE || op == gquery.OpGT || op == gquery.OpGTE ||
		op == gquery.OpLT || op == gquery.OpLTE || op == gquery.OpContains || op == gquery.OpPrefix
	if !validOp {
		return fmt.Errorf("%w: %q", ErrInvalidOperator, op)
	}
	if value == nil {
		if op == gquery.OpEQ || op == gquery.OpNE {
			return nil
		}
		return fmt.Errorf("%w: null only supports eq/ne", ErrInvalidValue)
	}
	if !path.hasField {
		return validateSystemValue(op, path.fieldName, value)
	}
	operations := c.service.types.FieldQueryOps(path.field)
	switch op {
	case gquery.OpEQ, gquery.OpNE:
		if !operations.Equal {
			return fmt.Errorf("%w: %s does not support %s", ErrInvalidOperator, path.fieldName, op)
		}
	case gquery.OpContains, gquery.OpPrefix:
		if !operations.Text {
			return fmt.Errorf("%w: %s does not support %s", ErrInvalidOperator, path.fieldName, op)
		}
	case gquery.OpGT, gquery.OpGTE, gquery.OpLT, gquery.OpLTE:
		if !operations.Ordered {
			return fmt.Errorf("%w: %s does not support %s", ErrInvalidOperator, path.fieldName, op)
		}
	}
	err := c.service.types.ValidateValue(typeName, path.field, value)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalidValue, path.fieldName, err)
	}
	return nil
}

func validateSystemValue(op gquery.CompareOp, field string, value any) error {
	textual := field == "type" || field == "display"
	ordered := field == "id" || field == "revision" || field == "created_at" || field == "updated_at" || field == "archived_at"
	if op == gquery.OpContains || op == gquery.OpPrefix {
		if !textual {
			return fmt.Errorf("%w: %s does not support %s", ErrInvalidOperator, field, op)
		}
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: %s expects string", ErrInvalidValue, field)
		}
		return nil
	}
	if op == gquery.OpGT || op == gquery.OpGTE || op == gquery.OpLT || op == gquery.OpLTE {
		if !ordered {
			return fmt.Errorf("%w: %s does not support %s", ErrInvalidOperator, field, op)
		}
	}
	switch field {
	case "type", "display":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: %s expects string", ErrInvalidValue, field)
		}
	case "id", "revision":
		if _, err := types.ToID(value); err != nil {
			return fmt.Errorf("%w: %s expects integer", ErrInvalidValue, field)
		}
	case "created_at", "updated_at", "archived_at":
		switch value.(type) {
		case time.Time, string:
		default:
			return fmt.Errorf("%w: %s expects time or string", ErrInvalidValue, field)
		}
	}
	return nil
}

func (c *queryCompiler) compileLogic(expr gquery.Logic, typeName, nodeRef string, depth int) (dba.Node, error) {
	if expr.Op == gquery.OpNot {
		if len(expr.Args) != 1 {
			return dba.Node{}, fmt.Errorf("%w: not requires one argument", ErrInvalidQuery)
		}
		child, err := c.compile(expr.Args[0], typeName, nodeRef, depth+1)
		if err != nil {
			return dba.Node{}, err
		}
		return dba.Expr("NOT (#{1})", child), nil
	}
	if expr.Op != gquery.OpAnd && expr.Op != gquery.OpOr {
		return dba.Node{}, fmt.Errorf("%w: logic operator %q", ErrInvalidOperator, expr.Op)
	}
	if len(expr.Args) == 0 {
		return dba.Node{}, fmt.Errorf("%w: %s requires arguments", ErrInvalidQuery, expr.Op)
	}
	children := make([]dba.Node, len(expr.Args))
	for i := range expr.Args {
		child, err := c.compile(expr.Args[i], typeName, nodeRef, depth+1)
		if err != nil {
			return dba.Node{}, err
		}
		children[i] = child
	}
	separator := " AND "
	if expr.Op == gquery.OpOr {
		separator = " OR "
	}
	parts := make([]string, len(children))
	args := make([]any, len(children))
	for i := range children {
		parts[i] = fmt.Sprintf("#{%d}", i+1)
		args[i] = children[i]
	}
	return dba.Expr("("+strings.Join(parts, separator)+")", args...), nil
}

func (c *queryCompiler) compileIn(expr gquery.In, typeName, nodeRef string) (dba.Node, error) {
	path, err := c.resolvePath(expr.Path, typeName, nodeRef)
	if err != nil {
		return dba.Node{}, err
	}
	values := append([]any(nil), expr.Values...)
	if expr.Set != nil {
		if expr.Path.Kind != gquery.PathOutRef && expr.Path.Kind != gquery.PathInRef {
			return dba.Node{}, fmt.Errorf("%w: set expressions require a relation path", ErrInvalidOperator)
		}
		values, err = c.resolveSet(expr.Set, path.target)
		if err != nil {
			return dba.Node{}, err
		}
	}
	if len(values) > gquery.MaxSetValues {
		return dba.Node{}, fmt.Errorf("%w: set exceeds %d values", ErrQueryTooComplex, gquery.MaxSetValues)
	}
	if len(values) == 0 {
		return dba.Expr("1 = 0"), nil
	}

	switch expr.Path.Kind {
	case gquery.PathSystem:
		return dba.Expr(path.sql+" IN (#{1|expand})", values), nil
	case gquery.PathField:
		if !c.service.types.FieldQueryOps(path.field).Equal {
			return dba.Node{}, fmt.Errorf("%w: %s does not support in", ErrInvalidOperator, path.fieldName)
		}
		for _, value := range values {
			if err := c.service.types.ValidateValue(typeName, path.field, value); err != nil {
				return dba.Node{}, fmt.Errorf("%w: %s: %v", ErrInvalidValue, path.fieldName, err)
			}
		}
		return dba.Expr(path.sql+" IN (#{1|expand})", values), nil
	case gquery.PathOutRef:
		if err := validateIDs(values); err != nil {
			return dba.Node{}, err
		}
		return dba.Expr(`EXISTS(SELECT 1 FROM edges e WHERE e.from_node = `+nodeRef+`.id AND e.field = #{1} AND e.to_node IN (#{2|expand}))`, path.fieldName, values), nil
	case gquery.PathInRef:
		if err := validateIDs(values); err != nil {
			return dba.Node{}, err
		}
		return dba.Expr(`EXISTS(SELECT 1 FROM edges e JOIN nodes src ON src.id = e.from_node WHERE e.to_node = `+nodeRef+`.id AND e.field = #{1} AND src.type = #{2} AND src.archived_at IS NULL AND e.from_node IN (#{3|expand}))`, path.fieldName, expr.Path.SourceType, values), nil
	default:
		return dba.Node{}, ErrInvalidField
	}
}

func (c *queryCompiler) compileExists(expr gquery.Exists, typeName, nodeRef string) (dba.Node, error) {
	path, err := c.resolvePath(expr.Path, typeName, nodeRef)
	if err != nil {
		return dba.Node{}, err
	}
	var node dba.Node
	switch expr.Path.Kind {
	case gquery.PathSystem:
		node = dba.Expr(path.sql + " IS NOT NULL")
	case gquery.PathField:
		node = dba.Expr(`json_type(`+nodeRef+`.fields, #{1}) IS NOT NULL`, "$."+path.fieldName)
	case gquery.PathOutRef:
		node = dba.Expr(`EXISTS(SELECT 1 FROM edges e JOIN nodes target ON target.id = e.to_node WHERE e.from_node = `+nodeRef+`.id AND e.field = #{1} AND target.archived_at IS NULL)`, path.fieldName)
	case gquery.PathInRef:
		node = dba.Expr(`EXISTS(SELECT 1 FROM edges e JOIN nodes src ON src.id = e.from_node WHERE e.to_node = `+nodeRef+`.id AND e.field = #{1} AND src.type = #{2} AND src.archived_at IS NULL)`, path.fieldName, expr.Path.SourceType)
	}
	if expr.Missing {
		return dba.Expr("NOT (#{1})", node), nil
	}
	return node, nil
}

func (c *queryCompiler) compileRelated(expr gquery.Related, typeName, nodeRef string, depth int) (dba.Node, error) {
	if depth >= maxRelationDepth {
		return dba.Node{}, fmt.Errorf("%w: relation depth exceeds %d", ErrQueryTooComplex, maxRelationDepth)
	}
	path, err := c.resolvePath(expr.Path, typeName, nodeRef)
	if err != nil {
		return dba.Node{}, err
	}
	if expr.Path.Kind != gquery.PathOutRef && expr.Path.Kind != gquery.PathInRef {
		return dba.Node{}, fmt.Errorf("%w: related requires ref path", ErrInvalidOperator)
	}
	if expr.Where == nil {
		return dba.Node{}, fmt.Errorf("%w: related where required", ErrInvalidQuery)
	}
	c.alias++
	alias := fmt.Sprintf("related_%d", c.alias)
	child, err := c.compile(expr.Where, path.target, alias, depth+1)
	if err != nil {
		return dba.Node{}, err
	}
	if expr.Path.Kind == gquery.PathOutRef {
		return dba.Expr(`EXISTS(SELECT 1 FROM edges e JOIN nodes `+alias+` ON `+alias+`.id = e.to_node WHERE e.from_node = `+nodeRef+`.id AND e.field = #{1} AND `+alias+`.archived_at IS NULL AND #{2})`, path.fieldName, child), nil
	}
	return dba.Expr(`EXISTS(SELECT 1 FROM edges e JOIN nodes `+alias+` ON `+alias+`.id = e.from_node WHERE e.to_node = `+nodeRef+`.id AND e.field = #{1} AND `+alias+`.type = #{2} AND `+alias+`.archived_at IS NULL AND #{3})`, path.fieldName, expr.Path.SourceType, child), nil
}

func (c *queryCompiler) resolveSet(set gquery.Set, expectedType string) ([]any, error) {
	switch value := set.(type) {
	case gquery.Subtree:
		root, err := c.service.GetNodeByAddress(value.Address)
		if err != nil {
			return nil, err
		}
		if root == nil {
			return nil, fmt.Errorf("%w: subtree address %q not found", ErrInvalidValue, value.Address)
		}
		if expectedType != "" && root.Type != expectedType {
			return nil, fmt.Errorf("%w: subtree type %q does not match %q", ErrInvalidValue, root.Type, expectedType)
		}
		tree, ok := c.service.types.Tree(root.Type)
		if !ok {
			return nil, fmt.Errorf("%w: type %q is not tree-enabled", ErrInvalidValue, root.Type)
		}
		ids, err := c.service.Subtree(root.Type, root.ID, tree.Parent, maxRelationDepth*5)
		if err != nil {
			return nil, err
		}
		values := make([]any, 0, len(ids)+1)
		values = append(values, root.ID)
		for _, id := range ids {
			values = append(values, id)
		}
		return values, nil
	default:
		return nil, fmt.Errorf("%w: unsupported set %T", ErrInvalidQuery, set)
	}
}

func validateIDs(values []any) error {
	for _, value := range values {
		id, err := types.ToID(value)
		if err != nil || id <= 0 {
			return fmt.Errorf("%w: relation values must be positive node IDs", ErrInvalidValue)
		}
	}
	return nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}
