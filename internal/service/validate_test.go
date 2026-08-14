package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func validateCall(src string) *taloonerpb.ValidateRulesetResponse {
	resp := New().Execute(plugin.Request{ID: "v", Action: "validate_ruleset", Args: map[string]string{"ruleset": src}})
	out := &taloonerpb.ValidateRulesetResponse{}
	if resp.StructuredContent != "" {
		_ = protojson.Unmarshal([]byte(resp.StructuredContent), out)
	}
	return out
}

func TestValidateRulesetValid(t *testing.T) {
	const src = `import "talooner.tln"

rule "Comment on drafts" {
  for records where type == "pr"
    and attr "pr.draft" == true
  do comment "pr" "draft"
  priority LOW
}`
	got := validateCall(src)
	if !got.Valid {
		t.Fatalf("expected valid, got diagnostics: %+v", got.Diagnostics)
	}
}

func TestValidateRulesetBadVerb(t *testing.T) {
	const src = `import "talooner.tln"

rule "Bad" {
  for records where type == "pr"
  do aprove "pr"
  priority LOW
}`
	got := validateCall(src)
	if got.Valid {
		t.Fatal("ruleset with unknown verb should be invalid")
	}
	joined := diagText(got.Diagnostics)
	if !strings.Contains(joined, "aprove") {
		t.Errorf("diagnostics should name the bad verb, got: %s", joined)
	}
}

func TestValidateRulesetEmpty(t *testing.T) {
	got := validateCall("")
	if got.Valid {
		t.Fatal("empty ruleset should be invalid")
	}
	if len(got.Diagnostics) == 0 {
		t.Fatal("empty ruleset should carry a diagnostic")
	}
}

func TestValidateRulesetCompileErrorHasPosition(t *testing.T) {
	const broken = `import "talooner.tln"

rule "unterminated" {
  for records where type == "pr"
`
	got := validateCall(broken)
	if got.Valid {
		t.Fatal("malformed ruleset should be invalid")
	}
	positioned := false
	for _, d := range got.Diagnostics {
		if d.Line > 0 {
			positioned = true
		}
	}
	if !positioned {
		t.Errorf("compile diagnostics should carry a line position: %+v", got.Diagnostics)
	}
}

func diagText(diags []*taloonerpb.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.Message)
		b.WriteString("\n")
	}
	return b.String()
}
