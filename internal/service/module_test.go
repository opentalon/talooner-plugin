package service

import (
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func TestPrimaryModuleByChangedLines(t *testing.T) {
	mods := []*taloonerpb.TouchedModule{
		{Name: "billing", ChangedLines: 10},
		{Name: "auth", ChangedLines: 200},
		{Name: "ui", ChangedLines: 5},
	}
	if p := primaryModule(mods); p.GetName() != "auth" {
		t.Errorf("primary = %q, want auth (most changed lines)", p.GetName())
	}
}

// Ties are broken by name (path) order for determinism.
func TestPrimaryModuleTieBrokenByName(t *testing.T) {
	mods := []*taloonerpb.TouchedModule{
		{Name: "zeta", ChangedLines: 100},
		{Name: "alpha", ChangedLines: 100},
		{Name: "mid", ChangedLines: 100},
	}
	if p := primaryModule(mods); p.GetName() != "alpha" {
		t.Errorf("tie primary = %q, want alpha (first in path order)", p.GetName())
	}
}

func TestPrimaryModuleEmpty(t *testing.T) {
	if primaryModule(nil) != nil {
		t.Error("no modules should yield no primary")
	}
}

func modulesEval(t *testing.T, ruleset, facts, modules string) *taloonerpb.EvaluatePrResponse {
	t.Helper()
	resp := New().Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		"repo": "acme/api", "pr": "1", "head_sha": "s", "ruleset": ruleset, "facts": facts, "modules": modules,
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

// module.* binds to the primary and the docs come from it; the PR is evaluated
// once. A rule reading module.name / module.documentation_urls sees the primary.
func TestEvaluateBindsPrimaryModule(t *testing.T) {
	// The rule matches only when module.name is bound to the primary; if it
	// fires, the binding is correct. (module.name is queried in the selector,
	// not interpolated — {attr.module.*} is subject to the same engine-side
	// interpolation limits as {item.*}.)
	const rule = `import "talooner.tln"

rule "Name the primary module" {
  for records where type == "pr" and attr "module.name" == "internal/auth"
  do comment "pr" "touches the auth module"
}`
	mods := `[
      {"name": "internal/auth", "changed_lines": 300, "documentation_urls": ["https://docs/auth"]},
      {"name": "billing", "changed_lines": 20, "documentation_urls": ["https://docs/billing"]}
    ]`
	out := modulesEval(t, rule, `{"pr.open": true}`, mods)

	var commented bool
	for _, a := range out.Actions {
		if a.Verb == taloonerpb.Verb_VERB_COMMENT {
			commented = true
		}
	}
	if !commented {
		t.Errorf("rule should bind module.name to the primary (internal/auth); actions=%+v", out.Actions)
	}
}

// module.touched_count lets a strict tenant require narrow PRs.
func TestModuleTouchedCount(t *testing.T) {
	const rule = `import "talooner.tln"

rule "Reject wide PRs" {
  for records where type == "pr" and attr "module.touched_count" > 1
  do comment "pr" "this PR spans multiple modules"
}`
	wide := `[{"name":"a","changed_lines":10},{"name":"b","changed_lines":5}]`
	if len(modulesEval(t, rule, "{}", wide).Actions) == 0 {
		t.Error("a multi-module PR should trip module.touched_count > 1")
	}

	narrow := `[{"name":"a","changed_lines":10}]`
	if len(modulesEval(t, rule, "{}", narrow).Actions) != 0 {
		t.Error("a single-module PR should not trip module.touched_count > 1")
	}
}

// Facts resolve as action arguments: `attr "user.owner"` passes the value, not
// the literal — without this the whole user.* namespace is dead weight.
func TestUserFactResolvesAsActionArg(t *testing.T) {
	const rule = `import "talooner.tln"

rule "Assign owner" {
  for records where type == "pr" and attr "pr.critical" == true
  do assign "pr" attr "user.owner"
}`
	out := modulesEval(t, rule, `{"pr.critical": true, "user.owner": "@team-lead"}`, "")
	var assignee string
	for _, a := range out.Actions {
		if a.Verb == taloonerpb.Verb_VERB_ASSIGN {
			assignee = a.Assignee
		}
	}
	if assignee != "@team-lead" {
		t.Errorf("assign should carry the resolved user.owner value, got %q", assignee)
	}
}
