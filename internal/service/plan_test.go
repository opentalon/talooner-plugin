package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

const planRuleset = `import "talooner.tln"

rule "Comment on critical" {
  for records where type == "pr" and attr "pr.critical" == true
  do comment "pr" "critical path"
  priority HIGH
}`

func evalRaw(s *Server, mode, facts string) plugin.Response {
	args := map[string]string{
		"repo": "acme/api", "pr": "5", "head_sha": "sha", "ruleset": planRuleset, "facts": facts,
	}
	if mode != "" {
		args["mode"] = mode
	}
	return s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: args})
}

// Plan mode returns the would-fire actions in the distinct `plan` field and
// leaves `actions` empty — the distinction is in the payload shape.
func TestPlanModePopulatesPlanNotActions(t *testing.T) {
	resp := evalRaw(New(), "plan", `{"pr.critical": true}`)
	if resp.Error != "" {
		t.Fatalf("plan errored: %s", resp.Error)
	}
	out := &taloonerpb.EvaluatePrResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatal(err)
	}
	if len(out.Plan) == 0 {
		t.Fatal("plan mode should populate plan[]")
	}
	if len(out.Actions) != 0 {
		t.Errorf("plan mode must leave actions[] empty, got %+v", out.Actions)
	}
}

// The payload itself must carry no executable `actions` key in plan mode, so a
// caller feeding it to an executor produces zero writes — the guarantee cannot
// rest on a convention.
func TestPlanModePayloadHasNoActionsKey(t *testing.T) {
	resp := evalRaw(New(), "plan", `{"pr.critical": true}`)
	if strings.Contains(resp.StructuredContent, "\"actions\"") {
		t.Errorf("plan-mode payload must not contain an actions key: %s", resp.StructuredContent)
	}
	if !strings.Contains(resp.StructuredContent, "\"plan\"") {
		t.Errorf("plan-mode payload should contain a plan key: %s", resp.StructuredContent)
	}
}

// Plan mode is a dry run: it persists no decision, so it can't pollute the
// audit trail of the real execute-mode decision.
func TestPlanModePersistsNothing(t *testing.T) {
	s := New()
	evalRaw(s, "plan", `{"pr.critical": true}`)
	if _, ok := s.decision("acme/api", 5, "sha"); ok {
		t.Error("plan mode must not persist a decision")
	}

	// Execute mode on the same PR does persist.
	evalRaw(s, "execute", `{"pr.critical": true}`)
	if _, ok := s.decision("acme/api", 5, "sha"); !ok {
		t.Error("execute mode should persist a decision")
	}
}

// Execute mode (and the default) still returns actions, not plan.
func TestExecuteModeUsesActions(t *testing.T) {
	for _, mode := range []string{"", "execute"} {
		resp := evalRaw(New(), mode, `{"pr.critical": true}`)
		out := &taloonerpb.EvaluatePrResponse{}
		if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
			t.Fatal(err)
		}
		if len(out.Actions) == 0 {
			t.Errorf("mode %q should return actions[]", mode)
		}
		if len(out.Plan) != 0 {
			t.Errorf("mode %q should leave plan[] empty", mode)
		}
	}
}
