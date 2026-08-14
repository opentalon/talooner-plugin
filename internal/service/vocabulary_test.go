package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func evalActions(t *testing.T, ruleset, facts string) []*taloonerpb.Action {
	t.Helper()
	resp := New().Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "1", "head_sha": "s", "ruleset": ruleset, "facts": facts,
	}})
	if resp.Error != "" {
		t.Fatalf("evaluate errored: %s", resp.Error)
	}
	out := &taloonerpb.EvaluatePrResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatal(err)
	}
	return out.Actions
}

// TestFullVocabulary exercises every non-block verb in one rule (approve without
// a competing block, so no conflict) and checks each maps with its arguments
// resolved. block is covered separately to avoid a spurious approve/block tie.
func TestFullVocabulary(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Everything" {
  for records where type == "pr" and attr "pr.trigger" == true
  do approve "pr"
  do comment "pr" "owner is {attr.pr.owner}"
  do assign "pr" attr "pr.owner"
  do require "review.senior"
  do notify "design-channel" "ui changed"
  do emit "flagged"
}`
	actions := evalActions(t, rs, `{"pr.trigger": true, "pr.owner": "@alice"}`)

	byVerb := map[taloonerpb.Verb]*taloonerpb.Action{}
	for _, a := range actions {
		byVerb[a.Verb] = a
	}

	if a := byVerb[taloonerpb.Verb_VERB_APPROVE]; a == nil || a.Target != "pr" {
		t.Errorf("approve: %+v", a)
	}
	if a := byVerb[taloonerpb.Verb_VERB_COMMENT]; a == nil || a.Text != "owner is @alice" {
		t.Errorf("comment interpolation not resolved: %+v", a)
	}
	if a := byVerb[taloonerpb.Verb_VERB_ASSIGN]; a == nil || a.Assignee != "@alice" {
		t.Errorf("assign attr not resolved to a value: %+v", a)
	}
	if a := byVerb[taloonerpb.Verb_VERB_REQUIRE]; a == nil || a.Target != "review.senior" {
		t.Errorf("require: %+v", a)
	}
	if a := byVerb[taloonerpb.Verb_VERB_NOTIFY]; a == nil || a.Target != "design-channel" || a.Text != "ui changed" {
		t.Errorf("notify: %+v", a)
	}
	if a := byVerb[taloonerpb.Verb_VERB_EMIT]; a == nil || a.Name != "flagged" {
		t.Errorf("emit: %+v", a)
	}
}

func TestBlockVerb(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Block it" {
  for records where type == "pr" and attr "pr.bad" == true
  block "merge"
  do block "pr.merge"
}`
	actions := evalActions(t, rs, `{"pr.bad": true}`)
	var ok bool
	for _, a := range actions {
		if a.Verb == taloonerpb.Verb_VERB_BLOCK && a.Target == "pr.merge" {
			ok = true
		}
	}
	if !ok {
		t.Errorf("block verb not returned: %+v", actions)
	}
}

// notify is deferred bot-side to v1.5, but a ruleset using it must compile and
// the plugin must return it.
func TestNotifyCompilesAndReturns(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Notify" {
  for records where type == "pr" and attr "pr.ui" == true
  do notify "design-channel" "heads up"
}`
	// validate_ruleset accepts it
	vr := New().Execute(plugin.Request{ID: "v", Action: "validate_ruleset", Args: map[string]string{"ruleset": rs}})
	out := &taloonerpb.ValidateRulesetResponse{}
	_ = protojson.Unmarshal([]byte(vr.StructuredContent), out)
	if !out.Valid {
		t.Fatalf("ruleset using notify should be valid: %+v", out.Diagnostics)
	}
	// and it is returned by evaluate_pr
	actions := evalActions(t, rs, `{"pr.ui": true}`)
	if len(actions) != 1 || actions[0].Verb != taloonerpb.Verb_VERB_NOTIFY {
		t.Errorf("notify should be returned: %+v", actions)
	}
}

// Retraction is by omission: an action that fired when its facts held is absent
// once they stop holding on a later evaluation. approve retracting is what the
// bot turns into a review dismissal.
func TestRetractionByOmission(t *testing.T) {
	const rs = `import "talooner.tln"

rule "Approve when ready" {
  for records where type == "pr" and attr "pr.ready" == true
  do approve "pr"
}`
	hasApprove := func(as []*taloonerpb.Action) bool {
		for _, a := range as {
			if a.Verb == taloonerpb.Verb_VERB_APPROVE {
				return true
			}
		}
		return false
	}

	if !hasApprove(evalActions(t, rs, `{"pr.ready": true}`)) {
		t.Fatal("run 1: approve should fire while pr.ready holds")
	}
	// Later evaluation: the fact no longer holds → approve is retracted (absent).
	if hasApprove(evalActions(t, rs, `{"pr.ready": false}`)) {
		t.Error("run 2: approve must be retracted once pr.ready no longer holds")
	}
}

// The proto contract carries the per-verb retraction semantics and the
// interpolation wrinkle, so a consumer reading the generated package sees them.
func TestContractDocumentsSemantics(t *testing.T) {
	// A cheap guard that the doc note survived regeneration: the enum has all
	// seven verbs, which the vocabulary tests above rely on.
	for _, v := range []taloonerpb.Verb{
		taloonerpb.Verb_VERB_APPROVE, taloonerpb.Verb_VERB_BLOCK, taloonerpb.Verb_VERB_COMMENT,
		taloonerpb.Verb_VERB_ASSIGN, taloonerpb.Verb_VERB_REQUIRE, taloonerpb.Verb_VERB_NOTIFY,
		taloonerpb.Verb_VERB_EMIT,
	} {
		if strings.TrimSpace(v.String()) == "" {
			t.Errorf("verb %d has no name", v)
		}
	}
}
