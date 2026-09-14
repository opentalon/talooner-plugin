package ruleset_test

import (
	"strings"
	"testing"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
)

func lintDiags(t *testing.T, src string) []ruleset.Diagnostic {
	t.Helper()
	return ruleset.CheckLLMResultCoverage(src)
}

// A rule that reacts to "mismatch" but never checks "unclear"/"error" leaves
// the tenant silent on both — the failure mode the design doc calls "the
// wrong way to fail silently".
func TestLLMResultCoverageWarnsOnMismatchOnly(t *testing.T) {
	const src = `rule "Block on mismatch" {
  for records where type == "code_unit"
    and attr "unit.llm_result" == "mismatch"
  do block "pr.merge"
}`
	diags := lintDiags(t, src)
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != ruleset.SeverityWarning {
		t.Errorf("severity = %v, want warning", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, "unclear") || !strings.Contains(diags[0].Message, "error") {
		t.Errorf("message should name the uncovered verdicts, got %q", diags[0].Message)
	}
}

func TestLLMResultCoverageSilentWithUnclearHandled(t *testing.T) {
	const src = `rule "Block on mismatch" {
  for records where type == "code_unit"
    and attr "unit.llm_result" == "mismatch"
  do block "pr.merge"
}

rule "Escalate on unclear" {
  for records where type == "code_unit"
    and attr "unit.llm_result" == "unclear"
  do notify "pr" "reviewer"
}`
	if diags := lintDiags(t, src); len(diags) != 0 {
		t.Fatalf("unclear handled, want no diagnostics, got %+v", diags)
	}
}

func TestLLMResultCoverageSilentWithErrorHandled(t *testing.T) {
	const src = `rule "Block on mismatch" {
  for records where type == "code_unit"
    and attr "unit.llm_result" == "mismatch"
  do block "pr.merge"
}

rule "Escalate on error" {
  for records where type == "code_unit"
    and attr "unit.llm_result" == "error"
  do notify "pr" "reviewer"
}`
	if diags := lintDiags(t, src); len(diags) != 0 {
		t.Fatalf("error handled, want no diagnostics, got %+v", diags)
	}
}

// A ruleset that never touches unit.llm_result at all has not opted into
// llm_review — nothing to warn about.
func TestLLMResultCoverageSilentWhenUnused(t *testing.T) {
	const src = `rule "Comment on drafts" {
  for records where type == "pr"
    and attr "pr.draft" == true
  do comment "pr" "still a draft"
}`
	if diags := lintDiags(t, src); len(diags) != 0 {
		t.Fatalf("no llm_result reference, want no diagnostics, got %+v", diags)
	}
}

// The scan must ignore a "mismatch" mention inside a comment or string.
func TestLLMResultCoverageIgnoresStringsAndComments(t *testing.T) {
	const src = `// attr "unit.llm_result" == "mismatch" should be ignored here
rule "Comment" {
  for records where type == "pr"
  do comment "pr" "attr \"unit.llm_result\" == \"mismatch\" in a string"
}`
	if diags := lintDiags(t, src); len(diags) != 0 {
		t.Fatalf("comment/string mention flagged, got %+v", diags)
	}
}

// End-to-end: the warning surfaces through Validate but never flips valid to
// false — it is a lint concern, not a compile error.
func TestValidateSurfacesLLMResultWarningWithoutInvalidating(t *testing.T) {
	const src = `import "talooner.tln"

rule "Block on mismatch" {
  for records where type == "code_unit"
    and attr "unit.llm_result" == "mismatch"
  do block "pr.merge"
  priority LOW
}`
	valid, diags := ruleset.Validate(src)
	if !valid {
		t.Fatalf("lint warning must not invalidate the ruleset, diags: %+v", diags)
	}
	var found bool
	for _, d := range diags {
		if d.Severity == ruleset.SeverityWarning && strings.Contains(d.Message, "unit.llm_result") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the unclear/error lint warning, got %+v", diags)
	}
}
