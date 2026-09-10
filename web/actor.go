package web

import (
	"errors"

	"github.com/kran/gcmv2/core"
)

// ActorKind identifies how a request was authenticated.
type ActorKind string

const (
	ActorAnonymous ActorKind = "anonymous"
	ActorNode      ActorKind = "node"
	ActorAdmin     ActorKind = "admin"
	ActorAPIKey    ActorKind = "api_key"
)

// Actor is request identity metadata, not a copy of a business entity.
type Actor struct {
	Kind     ActorKind `json:"kind"`
	NodeID   int64     `json:"node_id,omitempty"`
	NodeType string    `json:"node_type,omitempty"`
	Realm    string    `json:"realm,omitempty"`
	Scopes   []string  `json:"scopes,omitempty"`
}

// Authenticated reports whether the Actor represents accepted credentials.
func (a Actor) Authenticated() bool {
	return a.Kind != "" && a.Kind != ActorAnonymous
}

// ErrNoPrincipal means the current Actor is not backed by a Node.
var ErrNoPrincipal = errors.New("web: actor has no node principal")

// SetActor lets an authentication middleware install a verified Admin or API
// Key Actor. Node Actors should normally come from a Realm-bound Session.
func (c *CmsCtx) SetActor(actor Actor) {
	switch actor.Kind {
	case ActorNode:
		if actor.NodeID <= 0 || actor.NodeType == "" || actor.Realm == "" {
			panic("web: incomplete node actor")
		}
		realm, ok := c.site.auth.Realm(actor.Realm)
		if !ok || realm.NodeType != actor.NodeType {
			panic("web: node actor does not match a configured realm")
		}
	case ActorAdmin, ActorAPIKey:
	case ActorAnonymous:
		actor = Actor{Kind: ActorAnonymous}
	default:
		panic("web: unknown actor kind " + string(actor.Kind))
	}
	c.actor = actor
	c.actorLoaded = true
}

// Actor resolves the current request identity. Missing, invalid, expired, or
// Realm-inconsistent frontend credentials resolve to an explicit Anonymous Actor.
func (c *CmsCtx) Actor() Actor {
	if c.actorLoaded {
		return c.actor
	}
	c.actorLoaded = true
	c.actor = Actor{Kind: ActorAnonymous}

	token := c.authToken()
	if token == "" {
		return c.actor
	}
	session, err := c.site.engine.ValidSession(token)
	if err != nil || session == nil {
		return c.actor
	}
	realm, ok := c.site.auth.Realm(session.Realm)
	if !ok {
		return c.actor
	}
	node, err := c.site.engine.GetNodeById(session.NodeID)
	if err != nil || node == nil || node.ArchivedAt != nil || node.Type != realm.NodeType {
		return c.actor
	}
	c.principal = node
	c.principalLoaded = true
	c.actor = Actor{
		Kind: ActorNode, NodeID: node.ID, NodeType: node.Type, Realm: realm.Name,
	}
	return c.actor
}

// Principal returns the business Node behind a Node Actor.
func (c *CmsCtx) Principal() (*core.Node, error) {
	actor := c.Actor()
	if actor.Kind != ActorNode {
		return nil, ErrNoPrincipal
	}
	if c.principalLoaded {
		return c.principal, nil
	}
	node, err := c.site.engine.GetNodeById(actor.NodeID)
	if err != nil {
		return nil, err
	}
	if node == nil || node.Type != actor.NodeType {
		return nil, ErrNoPrincipal
	}
	c.principal = node
	c.principalLoaded = true
	return node, nil
}
