package web

import (
	"fmt"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// PolicyAction identifies a read operation whose row scope must be resolved.
type PolicyAction string

const (
	PolicyList   PolicyAction = "list"
	PolicyView   PolicyAction = "view"
	PolicySearch PolicyAction = "search"
	PolicyExport PolicyAction = "export"
)

// PolicyRequest is the stable input to a row-scope rule.
type PolicyRequest struct {
	Actor  Actor
	Action PolicyAction
	Type   string
}

// PolicyRule returns a mandatory AST scope. Returning nil denies all rows.
type PolicyRule func(*CmsCtx, PolicyRequest) (gquery.Expr, error)

type policyKey struct {
	typeName string
	action   PolicyAction
}

// PolicyRegistry stores server-side row-scope rules. Rules are immutable after
// Site.Start. Unregistered public read operations default to publication scope,
// or deny all rows when the Type has no publication capability.
type PolicyRegistry struct {
	site  *Site
	rules map[policyKey]PolicyRule
}

func newPolicyRegistry(site *Site) *PolicyRegistry {
	return &PolicyRegistry{site: site, rules: make(map[policyKey]PolicyRule)}
}

// Register installs one Type/action policy rule during Site configuration.
func (p *PolicyRegistry) Register(typeName string, action PolicyAction, rule PolicyRule) {
	if p.site.started {
		panic("web: register policy after Start")
	}
	if _, ok := p.site.engine.Types().Type(typeName); !ok {
		panic(fmt.Sprintf("web: policy type %q not defined", typeName))
	}
	if !validPolicyAction(action) {
		panic(fmt.Sprintf("web: invalid policy action %q", action))
	}
	if rule == nil {
		panic("web: nil policy rule")
	}
	key := policyKey{typeName: typeName, action: action}
	if _, exists := p.rules[key]; exists {
		panic(fmt.Sprintf("web: duplicate %s policy for %q", action, typeName))
	}
	p.rules[key] = rule
}

// Has reports whether the Site registered an explicit Type/action rule.
func (p *PolicyRegistry) Has(typeName string, action PolicyAction) bool {
	return p.rules[policyKey{typeName: typeName, action: action}] != nil
}

// Scope resolves and seals the mandatory Core query scope for one operation.
func (p *PolicyRegistry) Scope(ctx *CmsCtx, action PolicyAction, typeName string) (core.QueryScope, error) {
	if !validPolicyAction(action) {
		return core.QueryScope{}, fmt.Errorf("web: invalid policy action %q", action)
	}
	if _, ok := p.site.engine.Types().Type(typeName); !ok {
		return core.QueryScope{}, fmt.Errorf("web: policy type %q not defined", typeName)
	}
	request := PolicyRequest{Actor: ctx.Actor(), Action: action, Type: typeName}
	if rule := p.rules[policyKey{typeName: typeName, action: action}]; rule != nil {
		where, err := rule(ctx, request)
		if err != nil {
			return core.QueryScope{}, err
		}
		return core.PolicyScope(where), nil
	}
	publication, ok := p.site.engine.Types().Publication(typeName)
	if !ok {
		return core.PolicyScope(gquery.False()), nil
	}
	where := gquery.EQ(gquery.Field(publication.Field), publication.Published)
	return core.PolicyScope(where), nil
}

func validPolicyAction(action PolicyAction) bool {
	switch action {
	case PolicyList, PolicyView, PolicySearch, PolicyExport:
		return true
	default:
		return false
	}
}
