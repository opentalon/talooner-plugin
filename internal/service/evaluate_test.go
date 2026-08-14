package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// A tenant ruleset that imports the strict base and assigns the owner on a
// critical-path change — exercising per-row argument resolution.
const evalRuleset = `import "talooner.tln"

rule "Assign owner on critical path" {
  for records where type == "pr" and attr "pr.critical" == true
  do assign "pr" attr "user.owner"
  do comment "pr" "critical path touched"
  priority HIGH
}`

func evaluate(args map[string]string) plugin.Response {
	return New().Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: args})
}

func decodeEval(t *testing.T, resp plugin.Response) *taloonerpb.EvaluatePrResponse {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	out := &taloonerpb.EvaluatePrResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatalf("decode structured_content: %v", err)
	}
	return out
}

func TestEvaluatePRResolvesActionArgs(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo":     "acme/api",
		"pr":       "42",
		"head_sha": "abc123",
		"ruleset":  evalRuleset,
		"facts":    `{"pr.critical": true, "user.owner": "@alice"}`,
	})
	got := decodeEval(t, resp)

	var assign *taloonerpb.Action
	for _, a := range got.Actions {
		if a.Verb == taloonerpb.Verb_VERB_ASSIGN {
			assign = a
		}
	}
	if assign == nil {
		t.Fatalf("expected an assign action, got %+v", got.Actions)
	}
	// The point: `attr "user.owner"` arrives resolved to @alice, not the string
	// "user.owner".
	if assign.Assignee != "@alice" {
		t.Errorf("assignee = %q, want @alice (argument should be resolved per row)", assign.Assignee)
	}
	if got.Explain == nil || got.Explain.Summary == "" {
		t.Error("a decision should carry an explanation")
	}
}

// A ruleset that compiles and evaluates but matches nothing must return an empty
// action list and an explanation that says why — not an empty response.
func TestEvaluatePRFiresNothing(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo":    "acme/api",
		"pr":      "1",
		"ruleset": evalRuleset,
		"facts":   `{"pr.critical": false}`,
	})
	got := decodeEval(t, resp)
	if len(got.Actions) != 0 {
		t.Errorf("expected no actions, got %+v", got.Actions)
	}
	if got.Explain == nil || !strings.Contains(got.Explain.Summary, "no rules fired") {
		t.Errorf("explanation should say nothing fired, got %+v", got.Explain)
	}
}

func TestEvaluatePRMalformedFactsNamesField(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo":    "acme/api",
		"pr":      "1",
		"ruleset": evalRuleset,
		"facts":   `{"pr.changed_files": [1, 2]}`, // non-string list
	})
	if resp.Error == "" {
		t.Fatal("malformed facts should return an error, not a decision")
	}
	if !strings.Contains(resp.Error, "pr.changed_files") {
		t.Errorf("error should name the offending field, got %q", resp.Error)
	}
}

func TestEvaluatePRUnknownMode(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo": "acme/api", "pr": "1", "ruleset": evalRuleset, "facts": "{}",
		"mode": "destroy",
	})
	if resp.Error == "" || !strings.Contains(resp.Error, "mode") {
		t.Errorf("unknown mode should error naming mode, got %q", resp.Error)
	}
}

func TestEvaluatePRPlanModeNotYet(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo": "acme/api", "pr": "1", "ruleset": evalRuleset, "facts": "{}",
		"mode": "plan",
	})
	if resp.Error == "" || !strings.Contains(resp.Error, "plan") {
		t.Errorf("plan mode should error until P-C3, got %q", resp.Error)
	}
}

func TestEvaluatePRInvalidPR(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo": "acme/api", "pr": "not-a-number", "ruleset": evalRuleset, "facts": "{}",
	})
	if resp.Error == "" || !strings.Contains(resp.Error, "pr") {
		t.Errorf("non-numeric pr should error, got %q", resp.Error)
	}
}

// The strict base runs as part of every evaluation: a PR with unresolved
// conflicts is blocked by the base rule even though the tenant ruleset says
// nothing about conflicts.
func TestEvaluatePRStrictBaseFires(t *testing.T) {
	resp := evaluate(map[string]string{
		"repo":    "acme/api",
		"pr":      "9",
		"ruleset": evalRuleset,
		"facts":   `{"pr.mergeable": false}`,
	})
	got := decodeEval(t, resp)
	var blocked bool
	for _, a := range got.Actions {
		if a.Verb == taloonerpb.Verb_VERB_BLOCK && a.Target == "pr.merge" {
			blocked = true
		}
	}
	if !blocked {
		t.Errorf("strict base should block an unmergeable PR, got %+v", got.Actions)
	}
}
