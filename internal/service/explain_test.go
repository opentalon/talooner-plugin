package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

const explainRuleset = `import "talooner.tln"

rule "Comment on critical" {
  for records where type == "pr" and attr "pr.critical" == true
  do comment "pr" "critical path"
  priority HIGH
}`

func evalForExplain(s *Server, sha, facts string) {
	s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "5", "head_sha": sha, "ruleset": explainRuleset, "facts": facts,
	}})
}

func explain(s *Server, sha string) plugin.Response {
	return s.Execute(plugin.Request{ID: "x", Action: "explain_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "5", "head_sha": sha,
	}})
}

func TestExplainPRRendersPersistedDecision(t *testing.T) {
	s := New()
	evalForExplain(s, "sha1", `{"pr.critical": true}`)

	resp := explain(s, "sha1")
	if resp.Error != "" {
		t.Fatalf("explain errored: %s", resp.Error)
	}
	out := &taloonerpb.ExplainPrResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatal(err)
	}
	if out.Explain == nil || len(out.Explain.Firings) == 0 {
		t.Fatalf("explanation should record the fired rule, got %+v", out.Explain)
	}
	if out.Explain.Firings[0].Rule != "Comment on critical" {
		t.Errorf("wrong rule in explanation: %+v", out.Explain.Firings)
	}
}

// A sha that was never evaluated is a distinct, clear error — NOT an explain
// that reads like "no rules fired".
func TestExplainPRUnknownShaNotEvaluated(t *testing.T) {
	s := New()
	resp := explain(s, "never-seen")
	if resp.Error == "" {
		t.Fatal("explain for an unevaluated sha should be an error, not an empty explanation")
	}
	if !strings.Contains(resp.Error, "not evaluated") {
		t.Errorf("error should say it was not evaluated at that sha, got %q", resp.Error)
	}
	if resp.StructuredContent != "" {
		t.Error("an unevaluated sha must not return an explanation payload")
	}
}

// The two must be distinguishable: an evaluated sha that fired nothing renders
// a present explanation saying "no rules fired"; an unevaluated sha errors.
func TestExplainPRDistinguishesNothingFiredFromNotEvaluated(t *testing.T) {
	s := New()
	evalForExplain(s, "sha-quiet", `{"pr.critical": false}`) // evaluated, nothing fired

	quiet := explain(s, "sha-quiet")
	if quiet.Error != "" {
		t.Fatalf("an evaluated sha should render, got error %q", quiet.Error)
	}
	out := &taloonerpb.ExplainPrResponse{}
	_ = protojson.Unmarshal([]byte(quiet.StructuredContent), out)
	if out.Explain == nil || !strings.Contains(out.Explain.Summary, "no rules fired") {
		t.Errorf("nothing-fired should render 'no rules fired', got %+v", out.Explain)
	}

	unknown := explain(s, "sha-missing")
	if unknown.Error == "" {
		t.Error("an unevaluated sha should still error, distinct from nothing-fired")
	}
}

// The decision and its explanation outlive the facts: explain_pr renders even
// after the scope's facts have been cleared (as retention would).
func TestExplainPROutlivesFacts(t *testing.T) {
	s := New()
	evalForExplain(s, "sha1", `{"pr.critical": true}`)

	// Simulate a retention sweep of the scope's facts. The decision store is
	// separate, so the explanation must still render.
	s.factMu.Lock()
	for k := range s.tenantFacts {
		delete(s.tenantFacts, k)
	}
	s.factMu.Unlock()

	resp := explain(s, "sha1")
	if resp.Error != "" {
		t.Fatalf("explanation should outlive facts, got error %q", resp.Error)
	}
}

func TestExplainPRRequiresHeadSHA(t *testing.T) {
	resp := New().Execute(plugin.Request{ID: "x", Action: "explain_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "5",
	}})
	if resp.Error == "" || !strings.Contains(resp.Error, "head_sha") {
		t.Errorf("missing head_sha should error naming it, got %q", resp.Error)
	}
}
