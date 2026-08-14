package service

// Pure-function tests pinning the evaluator's two-valued semantics and the
// list-operand edges. Facts in, actions out — no GitHub, no runners, no API
// fixtures. The interesting cases are the ones that must NOT fire. If a future
// talon-language bump changes any of these, it surfaces here as a failure rather
// than as a silently wrong review.

import (
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func decide(t *testing.T, ruleset, facts string) (*taloonerpb.EvaluatePrResponse, *Server) {
	t.Helper()
	s := New()
	resp := s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "1", "head_sha": "sha", "ruleset": ruleset, "facts": facts,
	}})
	if resp.Error != "" {
		t.Fatalf("evaluate errored: %s", resp.Error)
	}
	out := &taloonerpb.EvaluatePrResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatal(err)
	}
	return out, s
}

func fired(out *taloonerpb.EvaluatePrResponse, v taloonerpb.Verb) bool {
	for _, a := range out.Actions {
		if a.Verb == v {
			return true
		}
	}
	return false
}

// A positive condition on an unset fact fails closed: mid-CI, pr.tests_passing
// is absent, so a rule gated on `== true` does not fire — and the rule is
// recorded among those that did not fire.
func TestUnsetFactFailsPositiveCondition(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Auto-approve when tests pass" {
  for records where type == "pr" and attr "pr.tests_passing" == true
  do approve "pr"
}`
	out, s := decide(t, rs, `{"pr.draft": false}`) // no pr.tests_passing
	if fired(out, taloonerpb.Verb_VERB_APPROVE) {
		t.Error("a rule gated on an unset fact == true must not fire")
	}
	d, _ := s.decision("acme/api", 1, "sha")
	if !contains(d.NotFired, "Auto-approve when tests pass") {
		t.Errorf("the non-firing rule should be recorded in the decision, got NotFired=%v", d.NotFired)
	}
}

// The accepted A1 risk, pinned: `not is "critical_path"` where critical_path is
// unset (extraction failed, no pr.changed_files) is TRUE under closed-world
// negation-as-failure, so the PR is approved without anything having checked it.
// If the engine ever goes three-valued, this test flips and we find out here.
func TestNegationAsFailureApprovesOnUnset(t *testing.T) {
	const rs = `import "talooner.tln"

define "critical_path" {
  attr "pr.changed_files" contains "internal/auth/"
}

rule "Auto-approve non-critical" {
  for records where type == "pr" and not is "critical_path"
  do approve "pr"
}`
	// pr.changed_files deliberately absent — the extractor threw.
	out, _ := decide(t, rs, `{"pr.draft": false}`)
	if !fired(out, taloonerpb.Verb_VERB_APPROVE) {
		t.Error("A1: `not is critical_path` on an unset fact is true, so approve must fire (v1 accepted risk)")
	}
}

// A list-valued pr.changed_files matches `contains` when ANY element does
// (talon-language#158).
func TestListContainsMatchesAnyElement(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Critical path" {
  for records where type == "pr" and attr "pr.changed_files" contains "internal/auth/"
  do require "review.senior"
}`
	out, _ := decide(t, rs, `{"pr.changed_files": ["README.md", "internal/auth/session.go"]}`)
	if !fired(out, taloonerpb.Verb_VERB_REQUIRE) {
		t.Error("contains should match when any list element contains the substring")
	}
}

// An empty list matches nothing — there is no fallback to a scalar path. Not
// obvious from the rule text, which is why it is pinned.
func TestEmptyListMatchesNothing(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Critical path" {
  for records where type == "pr" and attr "pr.changed_files" contains "internal/auth/"
  do require "review.senior"
}`
	out, _ := decide(t, rs, `{"pr.changed_files": []}`)
	if fired(out, taloonerpb.Verb_VERB_REQUIRE) {
		t.Error("an empty changed_files list must match no contains predicate")
	}
}

// `matches` is a substring scan, not a glob: `matches "**/*.css"` matches
// nothing because no path literally contains that text. Path predicates use
// contains / starts_with / ends_with.
func TestMatchesIsSubstringNotGlob(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Glob attempt" {
  for records where type == "pr" and attr "pr.changed_files" matches "**/*.css"
  do notify "design" "css changed"
}

rule "Substring works" {
  for records where type == "pr" and attr "pr.changed_files" ends_with ".css"
  do notify "design" "css changed"
}`
	out, _ := decide(t, rs, `{"pr.changed_files": ["app/assets/theme.css"]}`)
	// The glob rule matches nothing; the ends_with rule does — so exactly one
	// notify fires, proving the file is a .css yet the glob pattern did not hit.
	var notifies int
	for _, a := range out.Actions {
		if a.Verb == taloonerpb.Verb_VERB_NOTIFY {
			notifies++
		}
	}
	if notifies != 1 {
		t.Errorf("expected exactly one notify (ends_with, not the glob), got %d: %+v", notifies, out.Actions)
	}
}
