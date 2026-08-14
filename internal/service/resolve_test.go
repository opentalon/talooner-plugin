package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func evalDecision(t *testing.T, ruleset, facts string) *taloonerpb.EvaluatePrResponse {
	t.Helper()
	resp := New().Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "1", "head_sha": "sha", "ruleset": ruleset, "facts": facts,
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

func verbs(actions []*taloonerpb.Action) map[taloonerpb.Verb]bool {
	m := map[taloonerpb.Verb]bool{}
	for _, a := range actions {
		m[a.Verb] = true
	}
	return m
}

// Unhappy path 1: a strict base rule blocking the merge defeats a tenant
// approve — the approve must be absent from actions[], not returned for the bot
// to filter.
func TestStrictBaseDefeatsTenantApprove(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Approve anything ready" {
  for records where type == "pr" and attr "pr.ready" == true
  allow "merge"
  do approve "pr"
}`
	// pr.mergeable == false triggers the strict base block; pr.ready triggers
	// the tenant approve. Strict must win.
	got := evalDecision(t, rs, `{"pr.ready": true, "pr.mergeable": false}`)
	v := verbs(got.Actions)
	if v[taloonerpb.Verb_VERB_APPROVE] {
		t.Errorf("tenant approve should be defeated by the strict base block; actions=%+v", got.Actions)
	}
	if !v[taloonerpb.Verb_VERB_BLOCK] {
		t.Error("strict base block should be present")
	}
	for _, w := range got.Warnings {
		if w.Code == "unresolved_conflict" {
			t.Error("a strict defeat is resolved, so there should be no tie warning")
		}
	}
}

// Unhappy path 2: two tenant rules at equal priority, no overrides — both fire
// and a warning is present.
func TestEqualPriorityTieWarns(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Approver" {
  for records where type == "pr" and attr "pr.ready" == true
  allow "merge"
  do approve "pr"
  priority MEDIUM
}

rule "Blocker" {
  for records where type == "pr" and attr "pr.ready" == true
  block "merge"
  do block "pr.merge"
  priority MEDIUM
}`
	got := evalDecision(t, rs, `{"pr.ready": true}`)
	v := verbs(got.Actions)
	if !v[taloonerpb.Verb_VERB_APPROVE] || !v[taloonerpb.Verb_VERB_BLOCK] {
		t.Errorf("an unresolved tie returns both approve and block; actions=%+v", got.Actions)
	}
	var warned bool
	for _, w := range got.Warnings {
		if w.Code == "unresolved_conflict" && strings.Contains(w.Message, "Approver") && strings.Contains(w.Message, "Blocker") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("an unresolved tie must warn naming both rules; warnings=%+v", got.Warnings)
	}
}

// Higher priority defeats lower without an overrides edge (the layer the engine
// does not provide).
func TestHigherPriorityWins(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Approver low" {
  for records where type == "pr" and attr "pr.ready" == true
  allow "merge"
  do approve "pr"
  priority LOW
}

rule "Blocker high" {
  for records where type == "pr" and attr "pr.ready" == true
  block "merge"
  do block "pr.merge"
  priority HIGH
}`
	got := evalDecision(t, rs, `{"pr.ready": true}`)
	v := verbs(got.Actions)
	if v[taloonerpb.Verb_VERB_APPROVE] {
		t.Errorf("LOW approve should lose to HIGH block; actions=%+v", got.Actions)
	}
	if !v[taloonerpb.Verb_VERB_BLOCK] {
		t.Error("HIGH block should win")
	}
}

// Unhappy path 3: a transitive overrides chain A→B→C is resolved by the engine;
// the surviving decision is the un-overridden one.
func TestTransitiveOverrides(t *testing.T) {
	// C approve overrides B; B block overrides A; A approve. Only C's decision
	// (approve) should stand for the merge verdict, with no tie warning.
	const rs = `import "talooner.tln"

rule "A approve" {
  for records where type == "pr" and attr "pr.ready" == true
  allow "merge"
  do approve "pr"
}

rule "B block" {
  for records where type == "pr" and attr "pr.ready" == true
  block "merge"
  do block "pr.merge"
  overrides "A approve"
}

rule "C approve" {
  for records where type == "pr" and attr "pr.ready" == true
  allow "merge"
  do approve "pr"
  overrides "B block"
}`
	got := evalDecision(t, rs, `{"pr.ready": true}`)
	v := verbs(got.Actions)
	if v[taloonerpb.Verb_VERB_BLOCK] {
		t.Errorf("B block is overridden by C, should not survive; actions=%+v", got.Actions)
	}
	if !v[taloonerpb.Verb_VERB_APPROVE] {
		t.Errorf("C approve should stand; actions=%+v", got.Actions)
	}
	for _, w := range got.Warnings {
		if w.Code == "unresolved_conflict" {
			t.Errorf("overrides resolves the chain, so no tie warning; got %+v", got.Warnings)
		}
	}
}
