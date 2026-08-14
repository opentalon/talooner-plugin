// Package auth resolves a caller's API key to a tenant.
//
// The plugin endpoint is on the internet (deployment.md, "Exposing the
// cluster") and the API key is the whole gate: a key is scoped to one tenant,
// and if it leaks an attacker can burn that tenant's LLM budget and nothing
// else. Authentication fails closed — an unauthenticated caller learns nothing,
// not even whether a given tenant exists.
package auth

import (
	"errors"
	"strings"
)

// ArgAPIKey is the request arg the caller's tenant API key arrives in. It is a
// credential, not an action input, so it is not advertised as a parameter.
const ArgAPIKey = "api_key"

var (
	// ErrMissingKey means no API key was presented. Kept distinct from
	// ErrBadKey so a caller that forgot the credential gets a different
	// message than one presenting a wrong key — while neither reveals whether
	// any particular tenant exists.
	ErrMissingKey = errors.New("talooner: authentication required")

	// ErrBadKey means a key was presented but matches no tenant.
	ErrBadKey = errors.New("talooner: authentication failed")
)

// Tenant is one authenticated tenant's identity and capabilities.
type Tenant struct {
	Name     string
	Models   []string
	Features []string
	Quota    Quota
}

// Quota is the tenant's LLM budget usage for the current window.
type Quota struct {
	CallsUsed  int64
	CallsLimit int64
}

// HasFeature reports whether the tenant has a named feature (e.g. "llm_review"),
// which is how a caller learns at handshake time whether a capability is
// available before loading a ruleset that depends on it.
func (t Tenant) HasFeature(name string) bool {
	for _, f := range t.Features {
		if f == name {
			return true
		}
	}
	return false
}

// Registry resolves API keys to tenants. It is read-only after construction, so
// Authenticate is safe for concurrent use.
type Registry struct {
	byKey map[string]Tenant
}

// NewRegistry builds a registry from key→tenant pairs. It rejects an empty or
// duplicate key so a misconfiguration fails at startup rather than silently
// authenticating the wrong caller. A registry with no tenants is valid: it
// authenticates nobody, which is the correct fail-closed default before config
// is applied.
func NewRegistry(tenants map[string]Tenant) (*Registry, error) {
	byKey := make(map[string]Tenant, len(tenants))
	for key, t := range tenants {
		if strings.TrimSpace(key) == "" {
			return nil, errors.New("auth: tenant with empty api key")
		}
		if _, dup := byKey[key]; dup {
			return nil, errors.New("auth: api key configured for more than one tenant")
		}
		byKey[key] = t
	}
	return &Registry{byKey: byKey}, nil
}

// Configured reports whether any tenant is registered. A running plugin is
// always configured (the host calls Configure, which rejects an empty config),
// so this is false only for an as-yet-unconfigured Server.
func (r *Registry) Configured() bool { return len(r.byKey) > 0 }

// Authenticate resolves a presented key to its tenant. An empty key is
// ErrMissingKey; an unknown key is ErrBadKey. On success it returns the tenant.
func (r *Registry) Authenticate(key string) (Tenant, error) {
	if strings.TrimSpace(key) == "" {
		return Tenant{}, ErrMissingKey
	}
	t, ok := r.byKey[key]
	if !ok {
		return Tenant{}, ErrBadKey
	}
	return t, nil
}
