package ruleset_test

import (
	"testing"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
)

func TestRuleMetaReadsStrictAndPriority(t *testing.T) {
	const tenant = `import "talooner.tln"

rule "Default priority" {
  for records where type == "pr"
  do comment "pr" "hi"
}

strict rule "Strict high" {
  for records where type == "pr"
  do block "pr.merge"
  priority HIGH
}

rule "Low one" {
  for records where type == "pr"
  do approve "pr"
  priority LOW
}`
	meta := ruleset.RuleMeta(tenant)

	if m := meta["Default priority"]; m.Strict || m.Priority != ruleset.PriorityMedium {
		t.Errorf("default rule = %+v, want {Strict:false Priority:MEDIUM}", m)
	}
	if m := meta["Strict high"]; !m.Strict || m.Priority != ruleset.PriorityHigh {
		t.Errorf("strict high = %+v, want {Strict:true Priority:HIGH}", m)
	}
	if m := meta["Low one"]; m.Strict || m.Priority != ruleset.PriorityLow {
		t.Errorf("low rule = %+v, want {Strict:false Priority:LOW}", m)
	}
	// Strict base rules are CRITICAL and strict.
	if m := meta["Never approve a PR with unresolved conflicts"]; !m.Strict || m.Priority != ruleset.PriorityCritical {
		t.Errorf("base rule = %+v, want {Strict:true Priority:CRITICAL}", m)
	}
}
