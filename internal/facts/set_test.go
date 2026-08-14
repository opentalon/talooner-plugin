package facts_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/opentalon/talon-language/pkg/talon"

	"github.com/opentalon/talooner-plugin/internal/facts"
)

func TestBotOwns(t *testing.T) {
	owned := []string{"pr.draft", "user.owner", "repo.private", "review.state"}
	notOwned := []string{"preview.url", "llm_review.verdict", "module.x", "team.y", "event.z"}
	for _, a := range owned {
		if !facts.BotOwns(a) {
			t.Errorf("%q should be bot-owned", a)
		}
	}
	for _, a := range notOwned {
		if facts.BotOwns(a) {
			t.Errorf("%q should not be bot-owned", a)
		}
	}
}

// TestRederiveRetractsAbsentBotFacts is the core: a bot fact present before but
// absent from the new request is dropped, while non-bot facts survive.
func TestRederiveRetractsAbsentBotFacts(t *testing.T) {
	prior := facts.Set{
		"pr.draft":           true,       // bot, absent below → must drop
		"preview.url":        "https://", // tenant CI → must survive
		"llm_review.verdict": "ok",       // plugin, head-sha pinned → must survive
		"module.owner":       "@team",    // tenant lookup → must survive
	}
	request := facts.Set{"pr.mergeable": false}

	got := facts.Rederive(prior, request)
	want := facts.Set{
		"pr.mergeable":       false,
		"preview.url":        "https://",
		"llm_review.verdict": "ok",
		"module.owner":       "@team",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rederive =\n  %v\nwant\n  %v", got, want)
	}
	if _, present := got["pr.draft"]; present {
		t.Error("pr.draft was absent from the request and must have been retracted")
	}
}

func TestRederiveKeepsTenantFactsAcrossRuns(t *testing.T) {
	tenant := facts.Set{"preview.ready": true}
	run1 := facts.Rederive(tenant, facts.Set{"pr.draft": true})
	run2 := facts.Rederive(run1, facts.Set{"pr.mergeable": false})
	if run2["preview.ready"] != true {
		t.Errorf("tenant fact preview.ready did not survive two runs: %v", run2)
	}
}

func TestDecode(t *testing.T) {
	set, err := facts.Decode(`{"pr.draft": false, "pr.lines_changed": 600, "pr.owner": "@a", "pr.changed_files": ["x.go","y.go"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if set["pr.draft"] != false || set["pr.lines_changed"] != float64(600) || set["pr.owner"] != "@a" {
		t.Errorf("scalar decode wrong: %#v", set)
	}
	if !reflect.DeepEqual(set["pr.changed_files"], []string{"x.go", "y.go"}) {
		t.Errorf("list decode wrong: %#v", set["pr.changed_files"])
	}
}

func TestDecodeRejectsBadShapes(t *testing.T) {
	for _, blob := range []string{
		`{"pr.x": null}`,          // null is not a fact value
		`{"pr.x": {"nested": 1}}`, // nested object
		`{"pr.x": [1, 2]}`,        // non-string list
		`not json`,                // malformed
	} {
		if _, err := facts.Decode(blob); err == nil {
			t.Errorf("Decode(%s) should have failed", blob)
		}
	}
	// Empty blob is an empty set, not an error.
	if s, err := facts.Decode(""); err != nil || len(s) != 0 {
		t.Errorf("empty blob: set=%v err=%v", s, err)
	}
}

// TestReDerivationEndToEnd runs the engine across two runs to pin both unhappy
// paths: a fact present in run 1 and absent in run 2 stops matching, and a
// tenant-CI fact asserted out of band survives both.
func TestReDerivationEndToEnd(t *testing.T) {
	ctx := context.Background()

	const draftRule = `rule "draft" {
  for records where type == "pr" and attr "pr.draft" == true
  do emit "is_draft"
}`
	const previewRule = `rule "preview" {
  for records where type == "pr" and attr "preview.ready" == true
  do emit "has_preview"
}`
	fires := func(scope *facts.Scope, rule string) bool {
		res, err := talon.Run(ctx, rule, talon.WithFactStore(scope.Store()))
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		return len(res.Actions) > 0
	}

	tenant := facts.Set{"preview.ready": true} // asserted out of band by tenant CI

	// Run 1: PR is a draft.
	state1 := facts.Rederive(tenant, facts.Set{"pr.draft": true})
	s1 := facts.NewScope(facts.Key("acme/api", 7))
	if err := s1.Assert(ctx, state1); err != nil {
		t.Fatal(err)
	}
	if !fires(s1, draftRule) {
		t.Fatal("run 1: draft rule should fire")
	}
	if !fires(s1, previewRule) {
		t.Fatal("run 1: preview rule should fire")
	}

	// Run 2: PR is no longer a draft; pr.draft is absent from the request.
	state2 := facts.Rederive(state1, facts.Set{"pr.mergeable": false})
	s2 := facts.NewScope(facts.Key("acme/api", 7))
	if err := s2.Assert(ctx, state2); err != nil {
		t.Fatal(err)
	}
	if fires(s2, draftRule) {
		t.Error("run 2: pr.draft was retracted, draft rule must not fire")
	}
	if !fires(s2, previewRule) {
		t.Error("run 2: tenant preview.ready must survive re-derivation")
	}
}
