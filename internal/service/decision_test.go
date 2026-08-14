package service

import (
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
)

const decisionRuleset = `import "talooner.tln"

rule "Comment on critical path" {
  for records where type == "pr" and attr "pr.critical" == true
  do comment "pr" "critical"
  priority HIGH
}

rule "Never used here" {
  for records where type == "pr" and attr "pr.impossible" == true
  do comment "pr" "unreachable"
  priority LOW
}`

func evalForDecision(s *Server, headSHA, facts string) plugin.Response {
	return s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "42", "head_sha": headSHA, "ruleset": decisionRuleset, "facts": facts,
	}})
}

// The core guarantee: after evaluate_pr returns, the decision is already
// persisted and queryable — it is written before the response leaves, so a
// caller killed mid-flight still leaves an audit trail.
func TestEvaluatePersistsDecision(t *testing.T) {
	s := New()
	resp := evalForDecision(s, "sha1", `{"pr.critical": true}`)
	if resp.Error != "" {
		t.Fatalf("evaluate errored: %s", resp.Error)
	}

	d, ok := s.decision("acme/api", 42, "sha1")
	if !ok {
		t.Fatal("decision was not persisted")
	}
	if d.RulesetHash == "" {
		t.Error("decision missing ruleset hash")
	}
	if d.At == 0 {
		t.Error("decision missing timestamp")
	}
	if d.Facts["pr.critical"] != true {
		t.Errorf("decision did not snapshot facts: %v", d.Facts)
	}
	if len(d.Actions) == 0 {
		t.Error("decision missing returned actions")
	}
	if d.Explain == nil {
		t.Error("decision missing explain")
	}
}

// The decision records which rules fired and which did not — including the
// strict base rules that were evaluated but did not match.
func TestDecisionRecordsFiredAndNotFired(t *testing.T) {
	s := New()
	evalForDecision(s, "sha2", `{"pr.critical": true}`)
	d, ok := s.decision("acme/api", 42, "sha2")
	if !ok {
		t.Fatal("no decision")
	}

	if !contains(d.Fired, "Comment on critical path") {
		t.Errorf("expected the critical-path rule in Fired, got %v", d.Fired)
	}
	// A tenant rule whose condition was unmet, and the strict base rules, did
	// not fire and must be recorded as such.
	if !contains(d.NotFired, "Never used here") {
		t.Errorf("expected the unmatched rule in NotFired, got %v", d.NotFired)
	}
	if !contains(d.NotFired, "Never approve a PR with unresolved conflicts") {
		t.Errorf("expected the strict base rule in NotFired, got %v", d.NotFired)
	}
}

// Each reviewed sha keeps its own decision.
func TestDecisionKeyedByHeadSHA(t *testing.T) {
	s := New()
	evalForDecision(s, "shaA", `{"pr.critical": true}`)
	evalForDecision(s, "shaB", `{"pr.critical": false}`)

	a, _ := s.decision("acme/api", 42, "shaA")
	b, _ := s.decision("acme/api", 42, "shaB")
	if len(a.Actions) == 0 {
		t.Error("shaA should have a blocking/commenting action")
	}
	if len(b.Actions) != 0 {
		t.Errorf("shaB matched nothing and should have no actions, got %v", b.Actions)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
