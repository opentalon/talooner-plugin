// Package facts holds Talooner's per-PR fact scoping.
//
// "Scope" is a Talooner concept, not a talon-db one: the store is keyed
// (entity_id, doc_id) with entity_id pinned to one tenant per client, so a scope
// is one document per PR, keyed {repo}#{number} (facts.md, "Scoping and
// lifetime").
//
// The design called for injecting a pr_key pattern into every selector at load
// time. The public talon-language SDK exposes neither the AST nor an
// implementable FactStore, and selectors have no grouping parens — so a source
// rewrite cannot scope an `or`-selector without leaking. Instead each evaluation
// runs against its own FactStore, seeded with only that PR's facts. This is
// per-eval isolation: it is stronger than selector injection and independent of
// rule shape, because a rule cannot query records that are not present in the
// store. Another PR's facts live in another Scope and are never in the same
// store.
package facts

import (
	"context"
	"fmt"

	"github.com/opentalon/talon-language/pkg/talon"
)

// KeyAttr is the record attribute that carries a fact's scope key in the
// persistent (talon-db) store, so a PR's document can be identified and swept.
// Per-eval isolation does not depend on it; the fact-assertion path (P-B6) sets
// it when writing facts for durability and retention.
const KeyAttr = "pr_key"

// Key returns the scope key for a pull request: "{repo}#{number}". It is the
// talon-db doc_id for the PR's document.
func Key(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// Scope is one pull request's isolated fact store. An evaluation runs against
// Store(), so it can only ever see this PR's records.
type Scope struct {
	key   string
	store talon.FactStore
}

// NewScope creates an empty scope for the given key (see Key).
func NewScope(key string) *Scope {
	return &Scope{key: key, store: talon.NewMemoryStore()}
}

// Key reports the scope key.
func (s *Scope) Key() string { return s.key }

// Store returns the isolated fact store to run an evaluation against.
func (s *Scope) Store() talon.FactStore { return s.store }

// SeedGiven loads facts into this scope. body is a .tln.test `given` body — the
// record/attr statements without the surrounding test/given braces. It is the
// seam the fact-assertion path (P-B6) and evaluate_pr (P-B7) build on; the
// full-re-derivation semantics layer on top there. Returns the number of
// records written.
func (s *Scope) SeedGiven(ctx context.Context, body string) (int, error) {
	src := fmt.Sprintf("test %q {\n  given {\n%s\n  }\n}", "scope "+s.key, body)
	return talon.Seed(ctx, s.store, src)
}
