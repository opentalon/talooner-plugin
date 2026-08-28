package ruleset

import (
	"strings"
	"testing"
)

func hasWarning(diags []Diagnostic, substr string) bool {
	for _, d := range diags {
		if d.Severity == SeverityWarning && strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

// A rule that both reads llm_review.* and fires do llm_review can never see the
// result it produces — the lint must catch it.
func TestLintReadAndFireSameRule(t *testing.T) {
	src := `import "talooner.tln"

rule "read and fire" {
  for records where type == "pr" and attr "llm_review.result" == "mismatch"
  do llm_review "https://docs"
}`
	diags := LintLLMReview(src)
	if !hasWarning(diags, "both reads llm_review.* and fires llm_review") {
		t.Errorf("expected read+fire warning, got %+v", diags)
	}
}

// A producer and a separate consumer are the correct shape — no read+fire
// warning.
func TestLintProducerConsumerIsClean(t *testing.T) {
	src := `import "talooner.tln"

rule "producer" {
  for records where type == "pr" and attr "pr.needs_review" == true
  do llm_review "https://docs"
}

rule "consumer" {
  for records where type == "pr" and attr "llm_review.result" == "mismatch"
  do block "pr.merge"
}

rule "unclear" {
  for records where type == "pr" and attr "llm_review.result" == "unclear"
  do comment "pr" "inconclusive"
}

rule "error" {
  for records where type == "pr" and attr "llm_review.result" == "error"
  do comment "pr" "failed"
}`
	diags := LintLLMReview(src)
	if hasWarning(diags, "both reads llm_review.* and fires llm_review") {
		t.Errorf("producer/consumer split should not warn about read+fire, got %+v", diags)
	}
	if hasWarning(diags, "never handles") {
		t.Errorf("ruleset handles unclear and error; should not warn, got %+v", diags)
	}
}

// A ruleset that reads llm_review.result but ignores unclear/error fails
// silently when the model is unsure or the budget is gone.
func TestLintEnumCoverageWarnsOnMissingBranches(t *testing.T) {
	src := `import "talooner.tln"

rule "consumer" {
  for records where type == "pr" and attr "llm_review.result" == "mismatch"
  do block "pr.merge"
}`
	diags := LintLLMReview(src)
	if !hasWarning(diags, "never handles") {
		t.Errorf("expected enum-coverage warning, got %+v", diags)
	}
}

// A ruleset that never touches llm_review at all gets neither warning.
func TestLintNoLLMReviewNoWarnings(t *testing.T) {
	src := `import "talooner.tln"

rule "plain" {
  for records where type == "pr" and attr "pr.critical" == true
  do assign "pr" attr "user.owner"
}`
	if diags := LintLLMReview(src); len(diags) != 0 {
		t.Errorf("a ruleset without llm_review should produce no llm lint, got %+v", diags)
	}
}

// The lint is surfaced through Validate, and it is a warning — the ruleset is
// still valid.
func TestValidateSurfacesLintAsWarning(t *testing.T) {
	src := `import "talooner.tln"

rule "consumer" {
  for records where type == "pr" and attr "llm_review.result" == "mismatch"
  do block "pr.merge"
}`
	valid, diags := Validate(src)
	if !valid {
		t.Errorf("a lint warning must not make the ruleset invalid, got diags %+v", diags)
	}
	if !hasWarning(diags, "never handles") {
		t.Errorf("Validate should surface the enum-coverage warning, got %+v", diags)
	}
}
