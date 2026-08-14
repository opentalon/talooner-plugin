package ruleset_test

import (
	"strings"
	"testing"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
)

func verbErrors(t *testing.T, src string) []ruleset.Diagnostic {
	t.Helper()
	return ruleset.CheckVerbs(src)
}

// do aprove "pr" — a typo must be rejected by name. An unknown verb reaching
// the bot is a ruleset bug; one reaching nothing looks exactly like a rule that
// didn't match, which is the failure mode this rules out.
func TestVerbTypoRejectedByName(t *testing.T) {
	diags := verbErrors(t, `do aprove "pr"`)
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, `"aprove"`) {
		t.Errorf("message should name the offending verb, got %q", diags[0].Message)
	}
	for _, v := range ruleset.AllowedVerbs {
		if !strings.Contains(diags[0].Message, v) {
			t.Errorf("message should list valid verb %q, got %q", v, diags[0].Message)
		}
	}
}

// do deploy_preview / screenshot / scan_dependencies — parse fine as Tln, but
// are rejected with a pointer to the facts API rather than silently ignored.
func TestDispatchVerbsPointToFactsAPI(t *testing.T) {
	for _, verb := range []string{"deploy_preview", "screenshot", "scan_dependencies"} {
		diags := verbErrors(t, `do `+verb+` "pr"`)
		if len(diags) != 1 {
			t.Fatalf("%s: want 1 diagnostic, got %d", verb, len(diags))
		}
		if !strings.Contains(diags[0].Message, verb) {
			t.Errorf("%s: message should name the verb, got %q", verb, diags[0].Message)
		}
		if !strings.Contains(diags[0].Hint, "assert_facts") {
			t.Errorf("%s: hint should point at the facts API, got %q", verb, diags[0].Hint)
		}
	}
}

// do reject "pr" — "request changes" is how block renders on GitHub, not a
// separate verb.
func TestRejectVerbRejected(t *testing.T) {
	diags := verbErrors(t, `do reject "pr"`)
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(diags))
	}
	if !strings.Contains(diags[0].Hint, "block") {
		t.Errorf("reject hint should redirect to block, got %q", diags[0].Hint)
	}
}

func TestAllValidVerbsAccepted(t *testing.T) {
	var b strings.Builder
	for _, v := range ruleset.AllowedVerbs {
		b.WriteString(`do ` + v + ` "pr" "x"` + "\n")
	}
	if diags := verbErrors(t, b.String()); len(diags) != 0 {
		t.Fatalf("valid verbs produced diagnostics: %+v", diags)
	}
}

// The scan must not mistake a "do" inside a string or comment for an action.
func TestVerbScanIgnoresStringsAndComments(t *testing.T) {
	src := `// do frobnicate should be ignored
/* also do wobble in here */
do comment "pr" "please do frobnicate the widget"
do approve "pr"`
	if diags := verbErrors(t, src); len(diags) != 0 {
		t.Fatalf("scan flagged a verb inside a string/comment or a valid verb: %+v", diags)
	}
}

func TestVerbDiagnosticReportsPosition(t *testing.T) {
	src := "rule \"x\" {\n  do aprove \"pr\"\n}"
	diags := verbErrors(t, src)
	if len(diags) != 1 {
		t.Fatalf("want 1 diagnostic, got %d", len(diags))
	}
	if diags[0].Line != 2 {
		t.Errorf("line = %d, want 2", diags[0].Line)
	}
	if diags[0].Col != 6 { // "  do " → verb starts at column 6
		t.Errorf("col = %d, want 6", diags[0].Col)
	}
}

// TestValidateRejectsBadVerbInCompilableRuleset is the end-to-end point: a
// ruleset that tln-language compiles happily (it does not check verb names)
// is still invalid because the verb is outside the vocabulary.
func TestValidateRejectsBadVerbInCompilableRuleset(t *testing.T) {
	const src = `import "talooner.tln"

rule "Approve typo" {
  for records where type == "pr"
  do aprove "pr"
  priority LOW
}`
	valid, diags := ruleset.Validate(src)
	if valid {
		t.Fatal("ruleset with an unknown verb must be invalid")
	}
	var named bool
	for _, d := range diags {
		if strings.Contains(d.Message, `"aprove"`) {
			named = true
		}
	}
	if !named {
		t.Errorf("diagnostics should name the bad verb: %+v", diags)
	}
}

func TestValidateAcceptsGoodRuleset(t *testing.T) {
	const src = `import "talooner.tln"

rule "Comment on drafts" {
  for records where type == "pr"
    and attr "pr.draft" == true
  do comment "pr" "This is still a draft"
  priority LOW
}`
	valid, diags := ruleset.Validate(src)
	if !valid {
		t.Fatalf("well-formed ruleset should be valid, diags: %+v", diags)
	}
}
