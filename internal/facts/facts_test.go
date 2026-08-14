package facts_test

import (
	"context"
	"testing"

	"github.com/opentalon/talon-language/pkg/talon"

	"github.com/opentalon/talooner-plugin/internal/facts"
)

func TestKey(t *testing.T) {
	if got := facts.Key("acme/api", 42); got != "acme/api#42" {
		t.Errorf("Key = %q, want acme/api#42", got)
	}
}

// An or-selector: a record matches if EITHER arm holds. Naive selector
// injection cannot scope this without grouping parens, which Talon lacks — so
// it is exactly the rule shape that leaks if scoping is done wrong.
const orSelectorRule = `rule "flag" {
  for records where attr "pr.mergeable" == false or attr "pr.checks_pending" == true
  do comment "pr" attr "pr.owner"
}`

func runRule(t *testing.T, store talon.FactStore) []talon.Action {
	t.Helper()
	res, err := talon.Run(context.Background(), orSelectorRule, talon.WithFactStore(store))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res.Actions
}

func commentOwners(actions []talon.Action) []string {
	var owners []string
	for _, a := range actions {
		if a.Verb != "comment" {
			continue
		}
		// do comment "pr" attr "pr.owner" → args ["pr", <resolved owner>].
		if len(a.Args) >= 2 {
			if owner, ok := a.Args[1].(string); ok {
				owners = append(owners, owner)
			}
		}
	}
	return owners
}

// TestScopeIsolation is the point of P-B5: two PRs whose facts each match the
// or-selector, in the same repo. An evaluation scoped to PR 1 must see only PR
// 1's record — never PR 2's — regardless of the selector shape.
func TestScopeIsolation(t *testing.T) {
	ctx := context.Background()

	pr1 := facts.NewScope(facts.Key("acme/api", 1))
	if _, err := pr1.SeedGiven(ctx, `    record 1 type "pr"
    attr 1 "pr.mergeable" false
    attr 1 "pr.owner" "alice"`); err != nil {
		t.Fatal(err)
	}

	pr2 := facts.NewScope(facts.Key("acme/api", 2))
	if _, err := pr2.SeedGiven(ctx, `    record 1 type "pr"
    attr 1 "pr.checks_pending" true
    attr 1 "pr.owner" "bob"`); err != nil {
		t.Fatal(err)
	}

	owners1 := commentOwners(runRule(t, pr1.Store()))
	if len(owners1) != 1 || owners1[0] != "alice" {
		t.Fatalf("PR 1 evaluation saw %v, want exactly [alice] — PR 2's record leaked in", owners1)
	}

	owners2 := commentOwners(runRule(t, pr2.Store()))
	if len(owners2) != 1 || owners2[0] != "bob" {
		t.Fatalf("PR 2 evaluation saw %v, want exactly [bob]", owners2)
	}
}

// TestSharedStoreLeaks documents what per-eval isolation prevents: put both PRs'
// records in one store and the same or-selector matches both. This is the
// contamination Scope exists to make structurally impossible.
func TestSharedStoreLeaks(t *testing.T) {
	ctx := context.Background()
	shared := talon.NewMemoryStore()
	seed := `test "shared" {
  given {
    record 1 type "pr"
    attr 1 "pr.mergeable" false
    attr 1 "pr.owner" "alice"
    record 2 type "pr"
    attr 2 "pr.checks_pending" true
    attr 2 "pr.owner" "bob"
  }
}`
	if _, err := talon.Seed(ctx, shared, seed); err != nil {
		t.Fatal(err)
	}

	owners := commentOwners(runRule(t, shared))
	if len(owners) != 2 {
		t.Fatalf("shared store: got owners %v, expected both PRs to match (2) — the leak Scope prevents", owners)
	}
}
