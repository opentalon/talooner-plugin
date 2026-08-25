package service

import (
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func runTestCall(ruleset, testSource string) *taloonerpb.RunRulesetTestResponse {
	args := map[string]string{"ruleset": ruleset, "test_source": testSource}
	resp := New().Execute(plugin.Request{ID: "t", Action: "run_ruleset_test", Args: args})
	out := &taloonerpb.RunRulesetTestResponse{}
	if resp.StructuredContent != "" {
		_ = protojson.Unmarshal([]byte(resp.StructuredContent), out)
	}
	return out
}

const runTestRuleset = `import "talooner.tln"

rule "Comment on drafts" {
  for records where type == "pr"
    and attr "pr.draft" == true
  do comment "pr" "draft"
  priority LOW
}`

func TestRunRulesetTestPasses(t *testing.T) {
	const src = `test "Draft PR gets commented" {
  given {
    record 1 type "pr"
    attr 1 "pr.number" 1
    attr 1 "pr.draft" true
  }

  when rule "Comment on drafts"

  expect {
    did 1 comment "pr"
  }
}`
	got := runTestCall(runTestRuleset, src)
	if len(got.Diagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", got.Diagnostics)
	}
	if len(got.Results) != 1 {
		t.Fatalf("results = %d, want 1: %+v", len(got.Results), got.Results)
	}
	if !got.Results[0].Passed {
		t.Errorf("expected test to pass, errors: %v", got.Results[0].Errors)
	}
}

func TestRunRulesetTestFailingAssertion(t *testing.T) {
	const src = `test "Non-draft PR wrongly expected to be commented" {
  given {
    record 1 type "pr"
    attr 1 "pr.number" 1
    attr 1 "pr.draft" false
  }

  when rule "Comment on drafts"

  expect {
    did 1 comment "pr"
  }
}`
	got := runTestCall(runTestRuleset, src)
	if len(got.Results) != 1 {
		t.Fatalf("results = %d, want 1: %+v", len(got.Results), got.Results)
	}
	if got.Results[0].Passed {
		t.Error("expected test to fail")
	}
	if len(got.Results[0].Errors) == 0 {
		t.Error("failing test should carry an error message")
	}
}

func TestRunRulesetTestRulesetCompileError(t *testing.T) {
	const broken = `import "talooner.tln"

rule "unterminated" {
  for records where type == "pr"
`
	got := runTestCall(broken, `test "x" { given {} when rule "unterminated" expect {} }`)
	if len(got.Results) != 0 {
		t.Fatalf("expected no results on compile failure, got %+v", got.Results)
	}
	if len(got.Diagnostics) == 0 {
		t.Fatal("compile failure should carry diagnostics")
	}
}

func TestRunRulesetTestSourceParseError(t *testing.T) {
	got := runTestCall(runTestRuleset, `test "broken" { given {`)
	if len(got.Results) != 0 {
		t.Fatalf("expected no results on parse failure, got %+v", got.Results)
	}
	if len(got.Diagnostics) == 0 {
		t.Fatal("parse failure should carry diagnostics")
	}
}

func TestRunRulesetTestMissingRuleset(t *testing.T) {
	got := runTestCall("", `test "x" { given {} when rule "y" expect {} }`)
	if len(got.Diagnostics) == 0 {
		t.Fatal("missing ruleset should carry a diagnostic")
	}
}

func TestRunRulesetTestMissingTestSource(t *testing.T) {
	got := runTestCall(runTestRuleset, "")
	if len(got.Diagnostics) == 0 {
		t.Fatal("missing test_source should carry a diagnostic")
	}
}
