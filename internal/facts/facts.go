// Package facts holds Talooner's per-PR fact scoping.
//
// "Scope" is a Talooner concept, not a tln-db one: the store is keyed
// (entity_id, doc_id) with entity_id pinned to one tenant per client, so a scope
// is one document per PR, keyed {repo}#{number} (facts.md, "Scoping and
// lifetime").
//
// The design called for injecting a pr_key pattern into every selector at load
// time. The public tln-language SDK exposes neither the AST nor an
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

	"github.com/opentalon/tln-language/pkg/tln"
)

// KeyAttr is the record attribute that carries a fact's scope key in the
// persistent (tln-db) store, so a PR's document can be identified and swept.
// Per-eval isolation does not depend on it; the fact-assertion path (P-B6) sets
// it when writing facts for durability and retention.
const KeyAttr = "pr_key"

// Key returns the scope key for a pull request: "{repo}#{number}". It is the
// tln-db doc_id for the PR's document.
func Key(repo string, number int) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// Scope is one pull request's isolated fact store. An evaluation runs against
// Store(), so it can only ever see this PR's records.
type Scope struct {
	key   string
	store tln.FactStore
}

// NewScope creates an empty scope for the given key (see Key).
func NewScope(key string) *Scope {
	return &Scope{key: key, store: tln.NewMemoryStore()}
}

// Key reports the scope key.
func (s *Scope) Key() string { return s.key }

// Store returns the isolated fact store to run an evaluation against.
func (s *Scope) Store() tln.FactStore { return s.store }

// SeedGiven loads facts into this scope. body is a .tln.test `given` body — the
// record/attr statements without the surrounding test/given braces. Handy for
// tests; Assert is the production path.
func (s *Scope) SeedGiven(ctx context.Context, body string) (int, error) {
	src := fmt.Sprintf("test %q {\n  given {\n%s\n  }\n}", "scope "+s.key, body)
	return tln.Seed(ctx, s.store, src)
}

// prRecordID is the record id of the single PR record in a scope. A scope holds
// one document per PR (facts.md), so one record suffices; isolation comes from
// the scope, not the id.
const prRecordID = "1"

// Assert writes a fact Set into this scope as the PR record, tagged with
// pr_key = the scope key. It asserts EAV triples directly (rather than through
// the test DSL) so a fact value carrying newlines or quotes — e.g. pr.body —
// round-trips verbatim.
//
// Assert materialises a complete fact set; the caller is expected to pass the
// result of Rederive so that facts absent from the latest request are simply
// not present. Combined with P-B5's fresh per-evaluation store, that is how
// retraction happens without a store-level delete.
func (s *Scope) Assert(ctx context.Context, set Set) error {
	out := make([]tln.Fact, 0, len(set)+2)
	out = append(out,
		tln.Fact{RecordID: prRecordID, Attribute: ":record/type", Value: "pr"},
		tln.Fact{RecordID: prRecordID, Attribute: ":attr/" + KeyAttr, Value: s.key},
	)
	for attr, v := range set {
		fv, err := factValue(v)
		if err != nil {
			return fmt.Errorf("facts: attribute %q: %w", attr, err)
		}
		out = append(out, tln.Fact{RecordID: prRecordID, Attribute: ":attr/" + attr, Value: fv})
	}
	return s.store.Assert(ctx, out)
}

// factValue converts a Set value to the concrete type the fact store expects:
// numbers as float64, string lists as []any.
func factValue(v any) (any, error) {
	switch x := v.(type) {
	case bool, string, float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case []string:
		lst := make([]any, len(x))
		for i, s := range x {
			lst[i] = s
		}
		return lst, nil
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}
