package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func assertFacts(s *Server, repo, pr, factsJSON string) *taloonerpb.AssertFactsResponse {
	resp := s.Execute(plugin.Request{ID: "a", Action: "assert_facts", Args: map[string]string{
		"repo": repo, "pr": pr, "facts": factsJSON,
	}})
	out := &taloonerpb.AssertFactsResponse{}
	if resp.StructuredContent != "" {
		_ = protojson.Unmarshal([]byte(resp.StructuredContent), out)
	}
	return out
}

// One test per forbidden namespace — not a single happy path. Each reserved
// namespace must be rejected by name, since this is the only check that stands
// between tenant CI and defeating the ruleset.
func TestAssertFactsRejectsEachReservedNamespace(t *testing.T) {
	cases := map[string]string{
		"pr":         "pr.tests_passing",
		"user":       "user.owner",
		"repo":       "repo.private",
		"review":     "review.state",
		"event":      "event.deployed",
		"llm_review": "llm_review.verdict",
	}
	for ns, attr := range cases {
		t.Run(ns, func(t *testing.T) {
			got := assertFacts(New(), "acme/api", "1", `{"`+attr+`": true}`)
			if len(got.Accepted) != 0 {
				t.Errorf("%s should be rejected, not accepted", attr)
			}
			if len(got.Rejected) != 1 || got.Rejected[0].Attribute != attr {
				t.Fatalf("expected %q rejected, got %+v", attr, got.Rejected)
			}
			if !strings.Contains(got.Rejected[0].Reason, ns) {
				t.Errorf("rejection reason should name the namespace %q, got %q", ns, got.Rejected[0].Reason)
			}
		})
	}
}

// A permitted namespace is accepted, and the response carries no action list
// (AssertFactsResponse has no actions field — store-only by shape).
func TestAssertFactsAcceptsCustomNamespace(t *testing.T) {
	resp := New().Execute(plugin.Request{ID: "a", Action: "assert_facts", Args: map[string]string{
		"repo": "acme/api", "pr": "1", "facts": `{"preview.url": "https://x", "preview.ready": true}`,
	}})
	if resp.Error != "" {
		t.Fatalf("custom facts should be accepted: %s", resp.Error)
	}
	if strings.Contains(resp.StructuredContent, "\"actions\"") {
		t.Errorf("assert_facts response must carry no action list: %s", resp.StructuredContent)
	}
	out := &taloonerpb.AssertFactsResponse{}
	_ = protojson.Unmarshal([]byte(resp.StructuredContent), out)
	if len(out.Accepted) != 2 {
		t.Errorf("both custom facts should be accepted, got %+v", out.Accepted)
	}
}

// Mixed payload: permitted stored, forbidden reported AND not written.
func TestAssertFactsMixedPayload(t *testing.T) {
	s := New()
	got := assertFacts(s, "acme/api", "1", `{"preview.ready": true, "pr.tests_passing": true}`)
	if len(got.Accepted) != 1 || got.Accepted[0] != "preview.ready" {
		t.Errorf("accepted should be [preview.ready], got %+v", got.Accepted)
	}
	if len(got.Rejected) != 1 || got.Rejected[0].Attribute != "pr.tests_passing" {
		t.Errorf("rejected should be [pr.tests_passing], got %+v", got.Rejected)
	}
	// The rejected fact must not have been written.
	stored := s.tenantFactsFor("acme/api#1")
	if _, present := stored["pr.tests_passing"]; present {
		t.Error("a rejected fact must never be stored")
	}
	if stored["preview.ready"] != true {
		t.Error("the accepted fact should be stored")
	}
}

// The store-only path reaches a verdict: a fact asserted by CI is visible to a
// later evaluate_pr, and the rule fires on that run.
func TestAssertThenEvaluateReachesVerdict(t *testing.T) {
	const rule = `import "talooner.tln"

rule "React to preview" {
  for records where type == "pr" and attr "preview.ready" == true
  do comment "pr" "preview is up"
}`
	s := New()
	fires := func() bool {
		resp := s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
			"repo": "acme/api", "pr": "1", "head_sha": "sha", "ruleset": rule, "facts": "{}",
		}})
		if resp.Error != "" {
			t.Fatalf("evaluate errored: %s", resp.Error)
		}
		out := &taloonerpb.EvaluatePrResponse{}
		_ = protojson.Unmarshal([]byte(resp.StructuredContent), out)
		return len(out.Actions) > 0
	}

	if fires() {
		t.Fatal("rule should not fire before the fact is asserted")
	}
	assertFacts(s, "acme/api", "1", `{"preview.ready": true}`)
	if !fires() {
		t.Error("rule should fire at the next evaluate_pr once CI asserted the fact")
	}
}
