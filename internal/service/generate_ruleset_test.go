package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// A minimal ruleset/test pair that actually compiles and passes, standing in
// for a plausible model reply.
const validGeneratedRuleset = `rule "Block PRs with no description" {
  for records where type == "pr"
    and attr "pr.draft" == false
    and attr "pr.has_description" == false
  block "merge"
  do block "pr.merge"
  reason "no description"
}`

const validGeneratedTest = `test "PR with no description is blocked" {
  given {
    record 1 type "pr"
    attr 1 "pr.draft" false
    attr 1 "pr.has_description" false
  }

  when rule "Block PRs with no description"

  expect {
    flagged 1
    did 1 block "pr.merge"
  }
}`

func generateHosted(s *Server, host plugin.HostCaller, args map[string]string) plugin.Response {
	return s.ExecuteWithCallbacks(context.Background(),
		plugin.Request{ID: "g", Action: "generate_ruleset", Args: args}, host)
}

func decodeGenerate(t *testing.T, resp plugin.Response) *taloonerpb.GenerateRulesetResponse {
	t.Helper()
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	out := &taloonerpb.GenerateRulesetResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatalf("decode structured_content: %v", err)
	}
	return out
}

func baseGenerateArgs() map[string]string {
	return map[string]string{"repo_summary": "a Go CLI, cmd/ + internal/, go.mod present"}
}

func TestGenerateRulesetMissingSummaryErrors(t *testing.T) {
	resp := generateHosted(New(), &fakeHost{verdict: "unused"}, map[string]string{})
	if resp.Error == "" {
		t.Fatal("expected an error for a missing repo_summary")
	}
}

// The success path uses a fakeHost that returns the generate_ruleset JSON
// shape rather than llm_review's verdict shape — reusing fakeHost's plumbing
// (call counting) but its own reply. replies, if set, scripts one JSON reply
// per call in order (clamped to the last entry past the end) so a test can
// exercise the retry loop; otherwise every call returns the fixed
// ruleset/testSource fields.
type fakeGenerateHost struct {
	ruleset, testSource string
	replies             []generateReply
	calls               int
}

type generateReply struct {
	ruleset, testSource string
}

func (f *fakeGenerateHost) RunAction(_ context.Context, _, _ string, _ map[string]string) (plugin.CallResult, error) {
	f.calls++
	rs, ts := f.ruleset, f.testSource
	if len(f.replies) > 0 {
		idx := f.calls - 1
		if idx >= len(f.replies) {
			idx = len(f.replies) - 1
		}
		rs, ts = f.replies[idx].ruleset, f.replies[idx].testSource
	}
	return plugin.CallResult{
		StructuredContent: fmt.Sprintf(`{"ruleset":%q,"ruleset_test":%q}`, rs, ts),
	}, nil
}

func TestGenerateRulesetSuccessReturnsVerifiedPair(t *testing.T) {
	host := &fakeGenerateHost{ruleset: validGeneratedRuleset, testSource: validGeneratedTest}
	got := decodeGenerate(t, generateHosted(New(), host, baseGenerateArgs()))

	if got.GetSource() != "llm" {
		t.Errorf("source = %q, want llm", got.GetSource())
	}
	if got.GetRuleset() != validGeneratedRuleset {
		t.Errorf("ruleset mismatch")
	}
	if got.GetRulesetTest() != validGeneratedTest {
		t.Errorf("ruleset_test mismatch")
	}
	if host.calls != 1 {
		t.Errorf("want exactly one model call, got %d", host.calls)
	}
}

// A model reply that doesn't compile must retry with the compiler's
// complaint fed back, and only fall back once every attempt still fails —
// never handing the caller a known-broken pair.
func TestGenerateRulesetFallsBackOnInvalidRuleset(t *testing.T) {
	host := &fakeGenerateHost{ruleset: `rule "broken" { do }`, testSource: validGeneratedTest}
	got := decodeGenerate(t, generateHosted(New(), host, baseGenerateArgs()))

	if got.GetSource() != "fallback" {
		t.Errorf("source = %q, want fallback", got.GetSource())
	}
	if got.GetRuleset() != "" || got.GetRulesetTest() != "" {
		t.Error("fallback response must not carry a partial/broken pair")
	}
	if got.GetNote() == "" {
		t.Error("expected a note explaining the fallback")
	}
	if host.calls != maxGenerateAttempts {
		t.Errorf("want %d model calls (retrying a persistently broken reply) before giving up, got %d", maxGenerateAttempts, host.calls)
	}
}

// A model reply that compiles but fails its own test must also retry, then
// fall back if it never recovers.
func TestGenerateRulesetFallsBackOnFailingTest(t *testing.T) {
	failingTest := strings.Replace(validGeneratedTest, `did 1 block "pr.merge"`, `did_not 1 block "pr.merge"`, 1)
	host := &fakeGenerateHost{ruleset: validGeneratedRuleset, testSource: failingTest}
	got := decodeGenerate(t, generateHosted(New(), host, baseGenerateArgs()))

	if got.GetSource() != "fallback" {
		t.Errorf("source = %q, want fallback", got.GetSource())
	}
	if host.calls != maxGenerateAttempts {
		t.Errorf("want %d model calls before giving up, got %d", maxGenerateAttempts, host.calls)
	}
}

// A model that compiles cleanly after being shown its first attempt's
// compile error must succeed on the retry rather than exhausting every
// attempt or falling back.
func TestGenerateRulesetRecoversOnRetryAfterInvalidRuleset(t *testing.T) {
	host := &fakeGenerateHost{replies: []generateReply{
		{ruleset: `rule "broken" { do }`, testSource: validGeneratedTest},
		{ruleset: validGeneratedRuleset, testSource: validGeneratedTest},
	}}
	got := decodeGenerate(t, generateHosted(New(), host, baseGenerateArgs()))

	if got.GetSource() != "llm" {
		t.Errorf("source = %q, want llm — the retry produced a valid pair", got.GetSource())
	}
	if got.GetRuleset() != validGeneratedRuleset {
		t.Error("ruleset mismatch: want the corrected retry's ruleset")
	}
	if host.calls != 2 {
		t.Errorf("want exactly 2 model calls (1 broken + 1 corrected), got %d", host.calls)
	}
}

func TestGenerateRulesetNoHostFallsBack(t *testing.T) {
	got := decodeGenerate(t, generateHosted(New(), nil, baseGenerateArgs()))
	if got.GetSource() != "fallback" {
		t.Errorf("source = %q, want fallback", got.GetSource())
	}
}

// Unary Execute (standalone TCP mode) always passes a nil host.
func TestGenerateRulesetStandaloneFallsBack(t *testing.T) {
	s := New()
	s.SetStandalone(true)
	resp := s.Execute(plugin.Request{ID: "g", Action: "generate_ruleset", Args: baseGenerateArgs()})
	got := decodeGenerate(t, resp)
	if got.GetSource() != "fallback" {
		t.Errorf("source = %q, want fallback", got.GetSource())
	}
}

func TestGenerateRulesetQuotaExhaustedFallsBackWithoutCalling(t *testing.T) {
	const cfg = `{"tenants":[{"name":"acme","api_key":"k","features":["generate_ruleset"],"quota":{"calls_used":5,"calls_limit":5}}]}`
	s := New()
	if err := s.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	host := &fakeGenerateHost{ruleset: validGeneratedRuleset, testSource: validGeneratedTest}
	args := baseGenerateArgs()
	args[auth.ArgAPIKey] = "k"

	got := decodeGenerate(t, generateHosted(s, host, args))
	if got.GetSource() != "fallback" {
		t.Errorf("source = %q, want fallback", got.GetSource())
	}
	if host.calls != 0 {
		t.Errorf("exhausted quota must not call the model, got %d calls", host.calls)
	}
}

func TestGenerateRulesetConsumesQuotaOnSuccess(t *testing.T) {
	const cfg = `{"tenants":[{"name":"acme","api_key":"k","features":["generate_ruleset"],"quota":{"calls_used":0,"calls_limit":10}}]}`
	s := New()
	if err := s.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	host := &fakeGenerateHost{ruleset: validGeneratedRuleset, testSource: validGeneratedTest}
	args := baseGenerateArgs()
	args[auth.ArgAPIKey] = "k"
	generateHosted(s, host, args)

	tenant, _ := s.auth.Authenticate("k")
	if used := s.LLMCallsUsed(tenant); used != 1 {
		t.Errorf("one successful generation should consume one unit of quota, got %d", used)
	}
}

func TestWhoamiWithdrawsGenerateRulesetWhenStandalone(t *testing.T) {
	s := configured(t)
	s.SetStandalone(true)
	tenant, _ := s.auth.Authenticate("sekret")
	// The fixture config doesn't grant generate_ruleset; grant it here so the
	// withdrawal (not the absence) is what the test proves.
	tenant.Features = append(tenant.Features, "generate_ruleset")
	for _, f := range s.availableFeatures(tenant) {
		if f == "generate_ruleset" {
			t.Error("standalone mode must withdraw generate_ruleset from advertised features")
		}
	}
}
