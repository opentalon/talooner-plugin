package ruleset_test

import (
	"strings"
	"testing"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
)

func TestRuleNamesIncludesBaseAndTenant(t *testing.T) {
	const tenant = `import "talooner.tln"

// rule "in a comment" must be ignored
rule "Tenant one" {
  for records where type == "pr"
  do comment "pr" "rule \"in a string\" ignored too"
}

strict rule "Tenant two" {
  for records where type == "pr"
  do block "pr.merge"
}`
	names := ruleset.RuleNames(tenant)

	for _, want := range []string{
		"Never approve a PR with unresolved conflicts",          // base
		"Never approve while required checks are still running", // base
		"Tenant one",
		"Tenant two",
	} {
		if !containsName(names, want) {
			t.Errorf("RuleNames missing %q; got %v", want, names)
		}
	}
	for _, unwanted := range []string{"in a comment", "in a string ignored too"} {
		if containsName(names, unwanted) {
			t.Errorf("RuleNames wrongly captured %q from a comment/string; got %v", unwanted, names)
		}
	}
}

func TestHashStable(t *testing.T) {
	const src = `import "talooner.tln"
rule "x" { for records where type == "pr" do comment "pr" "hi" }`
	first, second := ruleset.Hash(src), ruleset.Hash(src)
	if first != second {
		t.Error("Hash not stable for identical source")
	}
	if ruleset.Hash(src) == ruleset.Hash(src+"\n") {
		t.Error("Hash should change with the source")
	}
}

func containsName(xs []string, want string) bool {
	for _, x := range xs {
		if strings.EqualFold(x, want) {
			return true
		}
	}
	return false
}
