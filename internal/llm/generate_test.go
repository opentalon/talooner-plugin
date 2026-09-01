package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
)

func TestGenerateNilHostIsNotOK(t *testing.T) {
	_, _, ok, explanation := Generate(context.Background(), nil, GenerateInput{RepoSummary: "a go cli"})
	if ok {
		t.Fatal("nil host should not be ok")
	}
	if explanation == "" {
		t.Error("expected an explanation naming the missing host")
	}
}

func TestGenerateParsesStructuredPayload(t *testing.T) {
	host := stubHost{res: plugin.CallResult{
		StructuredContent: `{"ruleset":"rule \"x\" {}","ruleset_test":"test \"y\" {}"}`,
	}}
	ruleset, testSource, ok, explanation := Generate(context.Background(), host, GenerateInput{RepoSummary: "a go cli"})
	if !ok {
		t.Fatalf("expected ok, got explanation %q", explanation)
	}
	if ruleset != "rule \"x\" {}\n" {
		t.Errorf("ruleset = %q, want a trailing newline appended", ruleset)
	}
	if testSource != "test \"y\" {}\n" {
		t.Errorf("testSource = %q, want a trailing newline appended", testSource)
	}
}

// A model that already ends its source with one or more newlines must not
// end up with a doubled or absent trailing newline.
func TestGenerateNormalizesTrailingNewlines(t *testing.T) {
	host := stubHost{res: plugin.CallResult{
		StructuredContent: `{"ruleset":"rule \"x\" {}\n\n\n","ruleset_test":"test \"y\" {}\n"}`,
	}}
	ruleset, testSource, ok, explanation := Generate(context.Background(), host, GenerateInput{RepoSummary: "a go cli"})
	if !ok {
		t.Fatalf("expected ok, got explanation %q", explanation)
	}
	if ruleset != "rule \"x\" {}\n" {
		t.Errorf("ruleset = %q, want exactly one trailing newline", ruleset)
	}
	if testSource != "test \"y\" {}\n" {
		t.Errorf("testSource = %q, want exactly one trailing newline", testSource)
	}
}

func TestGenerateFallsBackToContent(t *testing.T) {
	host := stubHost{res: plugin.CallResult{
		Content: "sure: {\"ruleset\":\"rule \\\"x\\\" {}\",\"ruleset_test\":\"test \\\"y\\\" {}\"}",
	}}
	_, _, ok, explanation := Generate(context.Background(), host, GenerateInput{RepoSummary: "a go cli"})
	if !ok {
		t.Fatalf("expected ok parsed from content, got explanation %q", explanation)
	}
}

// A ruleset containing braces inside its own body (every real tln ruleset
// does) must not confuse the balanced-object scan used to tolerate a
// prose-wrapped reply.
func TestGenerateTolerantOfBracesInsideRulesetBody(t *testing.T) {
	host := stubHost{res: plugin.CallResult{
		Content: "```json\n" +
			`{"ruleset":"rule \"x\" {\n  do approve \"pr\"\n}","ruleset_test":"test \"y\" {\n  expect { flagged 1 }\n}"}` +
			"\n```",
	}}
	ruleset, testSource, ok, explanation := Generate(context.Background(), host, GenerateInput{RepoSummary: "x"})
	if !ok {
		t.Fatalf("expected ok, got explanation %q", explanation)
	}
	if ruleset == "" || testSource == "" {
		t.Errorf("ruleset/testSource should not be empty: %q / %q", ruleset, testSource)
	}
}

func TestGenerateUnparseableIsNotOK(t *testing.T) {
	host := stubHost{res: plugin.CallResult{Content: "I could not decide"}}
	_, _, ok, explanation := Generate(context.Background(), host, GenerateInput{RepoSummary: "x"})
	if ok {
		t.Fatal("unparseable content should not be ok")
	}
	if explanation == "" {
		t.Error("expected an explanation")
	}
}

func TestGenerateEmptyFieldIsNotOK(t *testing.T) {
	host := stubHost{res: plugin.CallResult{StructuredContent: `{"ruleset":"","ruleset_test":"test \"y\" {}"}`}}
	_, _, ok, _ := Generate(context.Background(), host, GenerateInput{RepoSummary: "x"})
	if ok {
		t.Fatal("empty ruleset field should not be ok")
	}
}

func TestGenerateCallErrorIsNotOK(t *testing.T) {
	host := stubHost{err: errors.New("host boom")}
	_, _, ok, explanation := Generate(context.Background(), host, GenerateInput{RepoSummary: "x"})
	if ok {
		t.Fatal("a failed call should not be ok")
	}
	if explanation == "" {
		t.Error("expected an explanation")
	}
}
