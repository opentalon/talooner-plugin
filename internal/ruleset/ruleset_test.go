package ruleset_test

import (
	"strings"
	"testing"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
)

// A minimal well-formed tenant ruleset that imports the strict base.
const validTenant = `import "talooner.tln"

rule "Comment on drafts" {
  for records where type == "pr"
    and attr "pr.draft" == true
  do comment "pr" "This is still a draft"
  priority LOW
}`

func TestLoadValid(t *testing.T) {
	c, diags, err := ruleset.Load(validTenant)
	if err != nil {
		t.Fatalf("valid ruleset failed to load: %v\ndiags: %+v", err, diags)
	}
	if c.Hash == "" {
		t.Error("compiled ruleset has no content hash")
	}
}

// TestBaseCompiles proves the shipped strict base itself compiles, by loading a
// tenant that only imports it.
func TestBaseCompiles(t *testing.T) {
	const onlyImport = `import "talooner.tln"

rule "trivial" {
  for records where type == "pr"
  do comment "pr" "ok"
  priority LOW
}`
	if _, diags, err := ruleset.Load(onlyImport); err != nil {
		t.Fatalf("base ruleset does not compile: %v\ndiags: %+v", err, diags)
	}
}

// TestRedefiningImportedNameIsError is the one check standing between a tenant
// and deleting a safety rule: redefining a name from the imported strict base
// must be a compile error, naming the imported file and a line — not a silent
// replacement (tln-language#159).
func TestRedefiningImportedNameIsError(t *testing.T) {
	const shadow = `import "talooner.tln"

strict rule "Never approve a PR with unresolved conflicts" {
  for records where type == "pr"
  do comment "pr" "I have quietly deleted the safety rule"
  priority LOW
}`
	_, diags, err := ruleset.Load(shadow)
	if err == nil {
		t.Fatal("redefining an imported strict rule name must fail to compile")
	}

	var found bool
	for _, d := range diags {
		if d.Severity != ruleset.SeverityError {
			continue
		}
		if strings.Contains(d.Message, "Never approve a PR with unresolved conflicts") &&
			strings.Contains(d.Message, ruleset.BaseFileName) {
			if d.Line <= 0 {
				t.Errorf("shadow diagnostic has no line position: %+v", d)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error naming the redefined rule and the base file %q; got %+v",
			ruleset.BaseFileName, diags)
	}
}

func TestDiagnosticsCarryPositions(t *testing.T) {
	const broken = `import "talooner.tln"

rule "unterminated" {
  for records where type == "pr"
`
	_, diags, err := ruleset.Load(broken)
	if err == nil {
		t.Fatal("malformed ruleset should not compile")
	}
	positioned := false
	for _, d := range diags {
		if d.Line > 0 {
			positioned = true
		}
	}
	if !positioned {
		t.Errorf("expected at least one diagnostic with a line position, got %+v", diags)
	}
}

// TestHashDeterministicAndCoversInputs pins that the hash is stable for the same
// input and changes when the tenant changes — so a decision's recorded hash
// identifies the exact ruleset.
func TestHashDeterministicAndCoversInputs(t *testing.T) {
	a1, _, err := ruleset.Load(validTenant)
	if err != nil {
		t.Fatal(err)
	}
	a2, _, err := ruleset.Load(validTenant)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Hash != a2.Hash {
		t.Errorf("hash not deterministic: %s != %s", a1.Hash, a2.Hash)
	}

	other := strings.Replace(validTenant, "still a draft", "different text", 1)
	b, _, err := ruleset.Load(other)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Hash == b.Hash {
		t.Error("hash did not change when the tenant ruleset changed")
	}
}
