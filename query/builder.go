package query

func System(name string) Path { return Path{Kind: PathSystem, Field: name} }
func Field(name string) Path  { return Path{Kind: PathField, Field: name} }
func Ref(name string) Path    { return Path{Kind: PathOutRef, Field: name} }
func Incoming(sourceType, field string) Path {
	return Path{Kind: PathInRef, SourceType: sourceType, Field: field}
}

func EQ(path Path, value any) Expr  { return Compare{Op: OpEQ, Path: path, Value: value} }
func NE(path Path, value any) Expr  { return Compare{Op: OpNE, Path: path, Value: value} }
func GT(path Path, value any) Expr  { return Compare{Op: OpGT, Path: path, Value: value} }
func GTE(path Path, value any) Expr { return Compare{Op: OpGTE, Path: path, Value: value} }
func LT(path Path, value any) Expr  { return Compare{Op: OpLT, Path: path, Value: value} }
func LTE(path Path, value any) Expr { return Compare{Op: OpLTE, Path: path, Value: value} }
func Contains(path Path, value string) Expr {
	return Compare{Op: OpContains, Path: path, Value: value}
}
func Prefix(path Path, value string) Expr {
	return Compare{Op: OpPrefix, Path: path, Value: value}
}

func And(expressions ...Expr) Expr {
	return Logic{Op: OpAnd, Args: compact(expressions)}
}
func Or(expressions ...Expr) Expr {
	return Logic{Op: OpOr, Args: compact(expressions)}
}
func Not(expression Expr) Expr { return Logic{Op: OpNot, Args: []Expr{expression}} }

func OneOf(path Path, values ...any) Expr {
	copied := append([]any(nil), values...)
	return In{Path: path, Values: copied}
}
func InSet(path Path, set Set) Expr { return In{Path: path, Set: set} }
func IsNull(path Path) Expr         { return Exists{Path: path, Missing: true} }
func IsNotNull(path Path) Expr      { return Exists{Path: path} }
func RelatedTo(path Path, where Expr) Expr {
	return Related{Path: path, Where: where}
}
func SubtreeOf(address string) Set { return Subtree{Address: address} }
func True() Expr                   { return Constant(true) }
func False() Expr                  { return Constant(false) }

func Asc(path Path) SortField  { return SortField{Path: path} }
func Desc(path Path) SortField { return SortField{Path: path, Desc: true} }

func compact(expressions []Expr) []Expr {
	out := make([]Expr, 0, len(expressions))
	for _, expression := range expressions {
		if expression != nil {
			out = append(out, expression)
		}
	}
	return out
}
