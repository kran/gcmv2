// Package query defines the database-independent query AST and builders used by gcm.
package query

// PathKind identifies where a value is stored or which relation direction it uses.
type PathKind uint8

const (
	PathSystem PathKind = iota
	PathField
	PathOutRef
	PathInRef
)

// Path is a schema path. SourceType is required only for incoming references.
type Path struct {
	Kind       PathKind `json:"kind"`
	SourceType string   `json:"source_type,omitempty"`
	Field      string   `json:"field"`
}

// Expr is the closed filter AST. External packages construct values with builders.
type Expr interface {
	queryExpr()
}

type CompareOp string

const (
	OpEQ       CompareOp = "eq"
	OpNE       CompareOp = "ne"
	OpGT       CompareOp = "gt"
	OpGTE      CompareOp = "gte"
	OpLT       CompareOp = "lt"
	OpLTE      CompareOp = "lte"
	OpContains CompareOp = "contains"
	OpPrefix   CompareOp = "prefix"
)

type LogicOp string

const (
	OpAnd LogicOp = "and"
	OpOr  LogicOp = "or"
	OpNot LogicOp = "not"
)

// Compare compares a scalar path with one value.
type Compare struct {
	Op    CompareOp
	Path  Path
	Value any
}

func (Compare) queryExpr() {}

// Logic combines child expressions.
type Logic struct {
	Op   LogicOp
	Args []Expr
}

func (Logic) queryExpr() {}

// In tests a scalar or relation path against a bounded set.
type In struct {
	Path   Path
	Values []any
	Set    Set
}

func (In) queryExpr() {}

// Exists tests whether a value or relation exists. Missing negates the test.
type Exists struct {
	Path    Path
	Missing bool
}

func (Exists) queryExpr() {}

// Related opens a relation and applies Where to the related Node.
type Related struct {
	Path  Path
	Where Expr
}

func (Related) queryExpr() {}

// Constant is useful for policy composition and empty builder groups.
type Constant bool

func (Constant) queryExpr() {}

// Set is a closed set-producing AST used by In.
type Set interface {
	querySet()
}

// Subtree resolves an addressable tree node and returns its subtree IDs.
type Subtree struct {
	Address string
}

func (Subtree) querySet() {}

// SortField is a validated sort request. Stable ID ordering is appended by core.
type SortField struct {
	Path Path `json:"path"`
	Desc bool `json:"desc"`
}

// Page is numbered pagination. Number and Size must be positive when used.
type Page struct {
	Number int `json:"number"`
	Size   int `json:"size"`
}
