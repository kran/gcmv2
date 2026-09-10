package core

import (
	"fmt"

	gquery "github.com/kran/gcmv2/query"
)

// QueryScope is a mandatory, server-created row scope for list and search
// operations. Its fields are private so request decoders cannot manufacture a
// bypass value.
type QueryScope struct {
	where gquery.Expr
	mode  scopeMode
}

type scopeMode uint8

const (
	scopeUnset scopeMode = iota
	scopeRestricted
	scopeBypass
)

// PolicyScope creates an enforced row scope. A nil expression denies all rows;
// callers must use query.True explicitly when a policy allows every row.
func PolicyScope(where gquery.Expr) QueryScope {
	if where == nil {
		where = gquery.False()
	}
	return QueryScope{where: where, mode: scopeRestricted}
}

// BypassPolicy creates an explicit trusted-system/admin bypass. It is separate
// from PolicyScope(query.True()) so bypasses remain visible at call sites.
func BypassPolicy() QueryScope {
	return QueryScope{mode: scopeBypass}
}

func (s QueryScope) apply(userWhere gquery.Expr) (gquery.Expr, error) {
	switch s.mode {
	case scopeRestricted:
		if userWhere == nil {
			return s.where, nil
		}
		return gquery.And(userWhere, s.where), nil
	case scopeBypass:
		return userWhere, nil
	default:
		return nil, fmt.Errorf("%w: explicit policy scope required", ErrInvalidQuery)
	}
}
