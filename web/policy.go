package web

import (
	"fmt"
	"strings"

	"github.com/kran/gcmv2/core"
	gquery "github.com/kran/gcmv2/query"
)

// PolicyAction identifies one operation whose row scope must be resolved. The
// framework owns the system actions because it owns the code paths calling them.
// A Site may introduce its own action for a read surface no system action
// describes, and must namespace it with a dot (for example "site.my_content").
type PolicyAction string

const (
	PolicyList   PolicyAction = "list"
	PolicyView   PolicyAction = "view"
	PolicySearch PolicyAction = "search"
	PolicyExport PolicyAction = "export"
)

// PolicyDefault is the behaviour of a system action whose Type has no rule.
type PolicyDefault uint8

const (
	PolicyDefaultDeny          PolicyDefault = iota // no rows
	PolicyDefaultPublishedOnly                      // rows whose publication field is published
)

// policyDefaults is the one table defining every system action: what it falls
// back to when a Site registers no rule. Site actions are not listed here; they
// carry no default and must be registered with a rule, so an unregistered site
// action fails loud instead of silently falling back.
var policyDefaults = map[PolicyAction]PolicyDefault{
	PolicyList:   PolicyDefaultPublishedOnly,
	PolicyView:   PolicyDefaultPublishedOnly,
	PolicySearch: PolicyDefaultPublishedOnly,
	PolicyExport: PolicyDefaultPublishedOnly,
}

func (a PolicyAction) system() bool {
	_, ok := policyDefaults[a]
	return ok
}

// validAction accepts system actions (a rule may override them) and site actions
// namespaced with a dot, so a later framework action cannot silently collide
// with a name a Site already gave a different meaning.
func validAction(action PolicyAction) bool {
	return action.system() || strings.Contains(string(action), ".")
}

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

// PolicyRegistry stores server-side read rules. Rules are immutable after
// Site.Start. A system action with no registered rule falls back to its
// PolicyDefault; a site action with no rule for the Type is rejected.
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
	if !validAction(action) {
		panic(fmt.Sprintf("web: policy action %q must be a system action or contain a dot", action))
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

// Exposes reports whether the Site registered an explicit rule for the Type and
// action. It is what makes a Type without a publication capability publicly
// readable, and what the public routes and sitemap check before serving a Type.
func (p *PolicyRegistry) Exposes(typeName string, action PolicyAction) bool {
	return p.rules[policyKey{typeName: typeName, action: action}] != nil
}

// Scope resolves and seals the mandatory Core query scope for one read action.
func (p *PolicyRegistry) Scope(ctx *CmsCtx, action PolicyAction, typeName string) (core.QueryScope, error) {
	if _, ok := p.site.engine.Types().Type(typeName); !ok {
		return core.QueryScope{}, fmt.Errorf("web: policy type %q not defined", typeName)
	}
	if rule := p.rules[policyKey{typeName: typeName, action: action}]; rule != nil {
		request := PolicyRequest{Actor: ctx.Actor(), Action: action, Type: typeName}
		where, err := rule(ctx, request)
		if err != nil {
			return core.QueryScope{}, err
		}
		return core.PolicyScope(where), nil
	}
	if !action.system() {
		return core.QueryScope{}, fmt.Errorf("web: unknown policy action %q for type %q", action, typeName)
	}
	return p.defaultScope(typeName, action), nil
}

// defaultScope applies the system action's fallback for a Type with no rule.
// PublishedOnly needs the Type's publication capability; a Type without it has
// no publishable rows, so it is denied.
func (p *PolicyRegistry) defaultScope(typeName string, action PolicyAction) core.QueryScope {
	if policyDefaults[action] != PolicyDefaultPublishedOnly {
		return core.PolicyScope(gquery.False())
	}
	publication, ok := p.site.engine.Types().Publication(typeName)
	if !ok {
		return core.PolicyScope(gquery.False())
	}
	where := gquery.EQ(gquery.Field(publication.Field), publication.Published)
	return core.PolicyScope(where)
}
