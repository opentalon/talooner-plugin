package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
)

// A ruleset that fires several actions, so ordering is exercised.
const determinismRuleset = `import "talooner.tln"

rule "Big PR needs a senior and its owner" {
  for records where type == "pr" and attr "pr.big" == true
  do comment "pr" "large change"
  do require "review.senior"
  do assign "pr" attr "user.owner"
}`

func evalDet(s *Server, factsJSON string) plugin.Response {
	return s.Execute(plugin.Request{ID: "d", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "1", "head_sha": "sha", "ruleset": determinismRuleset, "facts": factsJSON,
	}})
}

// The determinism test — this is the product, not a nicety. The same facts and
// ruleset evaluated twice produce byte-identical actions. It holds because
// conflict resolution is defeasible (not load-order dependent) and the fact set
// is asserted from a map whose iteration order varies run to run, yet the engine
// orders actions by row and source position, not by assertion order.
func TestDeterminismByteIdentical(t *testing.T) {
	const facts = `{"pr.big": true, "user.owner": "@alice", "pr.lines_changed": 900}`

	// Fresh servers so the result is a function of the input, not of state.
	first := evalDet(New(), facts)
	if first.Error != "" {
		t.Fatalf("evaluate errored: %s", first.Error)
	}
	for i := 0; i < 25; i++ {
		got := evalDet(New(), facts)
		if got.StructuredContent != first.StructuredContent {
			t.Fatalf("non-deterministic decision on run %d:\n  %s\n  %s", i, first.StructuredContent, got.StructuredContent)
		}
	}
}

// Extend to the retraction path: evaluate, mutate one fact, re-evaluate, and the
// previously-fired action is absent from the second result. This is what makes
// "the PR grew past 500 lines" dismiss an approval.
func TestDeterminismRetractionPath(t *testing.T) {
	s := New()

	with := evalDet(s, `{"pr.big": true, "user.owner": "@alice"}`)
	if !strings.Contains(with.StructuredContent, "large change") {
		t.Fatalf("run 1 should fire the big-PR actions, got %s", with.StructuredContent)
	}

	// Mutate: the PR is no longer big. The bot re-sends the full fact set.
	without := evalDet(s, `{"pr.big": false, "user.owner": "@alice"}`)
	if strings.Contains(without.StructuredContent, "large change") {
		t.Errorf("run 2 must not carry the retracted action: %s", without.StructuredContent)
	}

	// And re-running the mutated input is itself deterministic.
	again := evalDet(s, `{"pr.big": false, "user.owner": "@alice"}`)
	if again.StructuredContent != without.StructuredContent {
		t.Errorf("re-evaluation of the mutated input is not byte-identical:\n  %s\n  %s",
			without.StructuredContent, again.StructuredContent)
	}
}
