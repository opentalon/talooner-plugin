package service

import (
	"testing"
	"time"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

const retentionRuleset = `import "talooner.tln"

rule "React to preview" {
  for records where type == "pr" and attr "preview.ready" == true
  do comment "pr" "preview up"
}

rule "Block unmergeable" {
  for records where type == "pr" and attr "pr.mergeable" == false
  block "merge"
  do block "pr.merge"
}`

func evalRet(t *testing.T, s *Server, factsJSON string) *taloonerpb.EvaluatePrResponse {
	t.Helper()
	resp := s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "7", "head_sha": "sha", "ruleset": retentionRuleset, "facts": factsJSON,
	}})
	if resp.Error != "" {
		t.Fatalf("evaluate errored: %s", resp.Error)
	}
	out := &taloonerpb.EvaluatePrResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasVerb(as []*taloonerpb.Action, v taloonerpb.Verb) bool {
	for _, a := range as {
		if a.Verb == v {
			return true
		}
	}
	return false
}

func TestSweepRemovesIdleFacts(t *testing.T) {
	s := New()
	assertFacts(s, "acme/api", "7", `{"preview.ready": true}`)

	// Well past the retention window.
	if n := s.Sweep(time.Now().Add(defaultFactRetention + 24*time.Hour)); n == 0 {
		t.Fatal("idle scope should have been swept")
	}
	if s.tenantFactsFor("acme/api#7") != nil {
		t.Error("stored facts should be gone after a sweep")
	}
}

func TestSweepKeepsRecentFacts(t *testing.T) {
	s := New()
	assertFacts(s, "acme/api", "7", `{"preview.ready": true}`)

	if n := s.Sweep(time.Now()); n != 0 {
		t.Errorf("recently-touched scope should not be swept, swept %d", n)
	}
	if s.tenantFactsFor("acme/api#7") == nil {
		t.Error("recent facts should survive")
	}
}

// Decisions outlive facts: a sweep removes facts but the decision stays
// queryable, so explain_pr still answers.
func TestSweepKeepsDecisions(t *testing.T) {
	s := New()
	evalRet(t, s, `{"pr.mergeable": false}`) // persists a decision at head_sha "sha"
	assertFacts(s, "acme/api", "7", `{"preview.ready": true}`)

	s.Sweep(time.Now().Add(defaultFactRetention + 24*time.Hour))

	if s.tenantFactsFor("acme/api#7") != nil {
		t.Error("facts should be swept")
	}
	if _, ok := s.decision("acme/api", 7, "sha"); !ok {
		t.Error("the decision must outlive the facts")
	}
}

// Idempotent + resumable: a second sweep of an already-swept store deletes
// nothing (no double-delete, no skip).
func TestSweepIdempotent(t *testing.T) {
	s := New()
	assertFacts(s, "acme/api", "7", `{"preview.ready": true}`)
	future := time.Now().Add(defaultFactRetention + 24*time.Hour)

	first := s.Sweep(future)
	second := s.Sweep(future)
	if first == 0 {
		t.Fatal("first sweep should remove the idle scope")
	}
	if second != 0 {
		t.Errorf("second sweep should be a no-op, swept %d", second)
	}
}

// A PR whose facts were swept re-derives cleanly on the next evaluate_pr — the
// bot re-sends the complete fact set, so the scope is whole, not half-empty.
func TestReopenedPRReDerivesCleanly(t *testing.T) {
	s := New()
	assertFacts(s, "acme/api", "7", `{"preview.ready": true}`)
	if !hasVerb(evalRet(t, s, `{}`).Actions, taloonerpb.Verb_VERB_COMMENT) {
		t.Fatal("preview rule should fire while the custom fact is stored")
	}

	// Facts swept; the custom fact is gone.
	s.Sweep(time.Now().Add(defaultFactRetention + 24*time.Hour))

	// Reopened and re-evaluated: the bot's complete fact set still drives a
	// clean decision (the strict base fires on an unmergeable PR); no error, no
	// stale half-scope.
	out := evalRet(t, s, `{"pr.mergeable": false}`)
	if !hasVerb(out.Actions, taloonerpb.Verb_VERB_BLOCK) {
		t.Errorf("re-derivation after a sweep should evaluate cleanly; actions=%+v", out.Actions)
	}
	// The swept preview fact should no longer fire its rule. (The base strict
	// rule also comments on an unmergeable PR, so match on text, not verb.)
	for _, a := range out.Actions {
		if a.Verb == taloonerpb.Verb_VERB_COMMENT && a.Text == "preview up" {
			t.Error("the swept preview fact should no longer fire its rule")
		}
	}
}

// Config cannot lower retention below the decision-20 floor.
func TestRetentionFloor(t *testing.T) {
	s := New()
	cfg := `{"tenants":[{"name":"acme","api_key":"k"}], "fact_retention_days": 1}`
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if s.factRetention != minFactRetention {
		t.Errorf("retention = %v, want it clamped up to the floor %v", s.factRetention, minFactRetention)
	}
}

func TestRetentionConfigured(t *testing.T) {
	s := New()
	cfg := `{"tenants":[{"name":"acme","api_key":"k"}], "fact_retention_days": 30}`
	if err := s.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if want := 30 * 24 * time.Hour; s.factRetention != want {
		t.Errorf("retention = %v, want %v", s.factRetention, want)
	}
}
