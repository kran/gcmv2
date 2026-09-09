package web

import (
	"fmt"
	"regexp"
)

var authRealmName = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// AuthRealm maps a stable public authentication realm to an internal Node type.
type AuthRealm struct {
	Name          string `json:"name"`
	NodeType      string `json:"node_type"`
	AllowRegister bool   `json:"allow_register"`
	Default       bool   `json:"default"`
}

// AuthRegistry owns the immutable authentication configuration of a Site.
type AuthRegistry struct {
	site        *Site
	realms      map[string]AuthRealm
	defaultName string
}

func newAuthRegistry(site *Site) *AuthRegistry {
	return &AuthRegistry{site: site, realms: make(map[string]AuthRealm)}
}

// Register adds a Realm during Site configuration. Invalid configuration
// panics so the application cannot start with an ambiguous authentication map.
func (r *AuthRegistry) Register(realm AuthRealm) {
	if r.site.started {
		panic("web: register auth realm after Start")
	}
	if !authRealmName.MatchString(realm.Name) {
		panic(fmt.Sprintf("web: invalid auth realm name %q", realm.Name))
	}
	if _, exists := r.realms[realm.Name]; exists {
		panic(fmt.Sprintf("web: duplicate auth realm %q", realm.Name))
	}
	td, ok := r.site.engine.Types().Type(realm.NodeType)
	if !ok {
		panic(fmt.Sprintf("web: auth realm %q: node type %q not defined", realm.Name, realm.NodeType))
	}
	if !td.Capabilities.Authentication {
		panic(fmt.Sprintf("web: auth realm %q: node type %q is not auth-enabled", realm.Name, realm.NodeType))
	}
	if realm.Default && r.defaultName != "" {
		panic(fmt.Sprintf("web: auth realms %q and %q are both default", r.defaultName, realm.Name))
	}
	r.realms[realm.Name] = realm
	if realm.Default {
		r.defaultName = realm.Name
	}
}

// Realm returns a configured Realm by public name.
func (r *AuthRegistry) Realm(name string) (AuthRealm, bool) {
	realm, ok := r.realms[name]
	return realm, ok
}

// MustRealm returns a configured Realm or panics during application assembly.
func (r *AuthRegistry) MustRealm(name string) AuthRealm {
	realm, ok := r.Realm(name)
	if !ok {
		panic(fmt.Sprintf("web: auth realm %q not registered", name))
	}
	return realm
}

// DefaultRealm returns the explicitly configured default Realm.
func (r *AuthRegistry) DefaultRealm() (AuthRealm, bool) {
	if r.defaultName == "" {
		return AuthRealm{}, false
	}
	return r.Realm(r.defaultName)
}
