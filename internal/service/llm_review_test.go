package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// fakeHost is a HostCaller that returns a canned _subprocess reply and counts
// calls, so the llm_review path is exercised without a real model. It stands in
// for VCR cassettes: the host owns the provider call, so the plugin's tests need
// only a deterministic host.
type fakeHost struct {
	mu      sync.Mutex
	calls   int
	verdict string
	explain string
	err     error
}

func (f *fakeHost) RunAction(_ context.Context, _, _ string, _ map[string]string) (plugin.CallResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return plugin.CallResult{}, f.err
	}
	return plugin.CallResult{
		StructuredContent: fmt.Sprintf(`{"verdict":%q,"explanation":%q}`, f.verdict, f.explain),
	}, nil
}

func (f *fakeHost) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// An enrich block reviews each important code unit via the native tool step; a
// consumer rule reacts to the verdict asserted onto that code_unit record. This
// is the shape llm-review.md describes.
const llmRuleset = `import "talooner.tln"

enrich "Review code units against docs" {
  for records where type == "code_unit" and attr "unit.important" == true
  stale_after 1 hour
  tool "llm" "review" {
    unit attr "unit.name"
    doc attr "unit.doc_content"
    diff attr "unit.diff"
  }
  update attr "unit.llm_result" from result.verdict
  update attr "unit.llm_explanation" from result.explanation
}

rule "Block on documented mismatch" {
  for records where type == "code_unit" and attr "unit.llm_result" == "mismatch"
  do block "pr.merge"
  do comment "pr" "Code contradicts the module docs"
}

rule "Comment when unclear" {
  for records where type == "code_unit" and attr "unit.llm_result" == "unclear"
  do comment "pr" "Review inconclusive"
}

rule "Comment on error" {
  for records where type == "code_unit" and attr "unit.llm_result" == "error"
  do comment "pr" "Review could not run"
}`

const codeUnitsJSON = `[{"name":"auth","important":true,"doc_url":"http://d","doc_content":"auth must hash passwords","diff":"- plaintext\n+ bcrypt"}]`

func evaluateHosted(s *Server, host plugin.HostCaller, args map[string]string) plugin.Response {
	return s.ExecuteWithCallbacks(context.Background(),
		plugin.Request{ID: "e", Action: "evaluate_pr", Args: args}, host)
}

func baseLLMArgs() map[string]string {
	return map[string]string{
		"repo":       "acme/api",
		"pr":         "1",
		"head_sha":   "sha1",
		"ruleset":    llmRuleset,
		"facts":      `{"pr.mergeable": true}`, // keep the strict base from blocking
		"code_units": codeUnitsJSON,
	}
}

func hasBlock(actions []*taloonerpb.Action, target string) bool {
	for _, a := range actions {
		if a.Verb == taloonerpb.Verb_VERB_BLOCK && a.Target == target {
			return true
		}
	}
	return false
}

func hasCommentContaining(actions []*taloonerpb.Action, substr string) bool {
	for _, a := range actions {
		if a.Verb == taloonerpb.Verb_VERB_COMMENT && strings.Contains(a.Text, substr) {
			return true
		}
	}
	return false
}

// The engine-native path: an enrich tool step reviews the unit, its verdict is
// asserted onto the code_unit record, and the consumer rule reaches its decision
// on the read pass — one model call, no llm verb in the returned actions.
func TestLLMReviewEnrichBlocksOnMismatch(t *testing.T) {
	host := &fakeHost{verdict: "mismatch", explain: "contradicts docs"}
	got := decodeEval(t, evaluateHosted(New(), host, baseLLMArgs()))

	if !hasBlock(got.Actions, "pr.merge") {
		t.Fatalf("consumer rule should block on mismatch, got %+v", got.Actions)
	}
	if host.count() != 1 {
		t.Errorf("want exactly one model call, got %d", host.count())
	}
}

// Determinism: a second evaluation at the same head sha replays the cached
// verdict without a second model call.
func TestLLMReviewCachedAtSameSHA(t *testing.T) {
	host := &fakeHost{verdict: "mismatch", explain: "x"}
	s := New()
	evaluateHosted(s, host, baseLLMArgs())
	got := decodeEval(t, evaluateHosted(s, host, baseLLMArgs()))

	if !hasBlock(got.Actions, "pr.merge") {
		t.Errorf("cached verdict should still drive the block, got %+v", got.Actions)
	}
	if host.count() != 1 {
		t.Errorf("same sha should reuse the cache: want 1 call, got %d", host.count())
	}
}

// force bypasses the cache and pays for a second call.
func TestLLMReviewForceBypassesCache(t *testing.T) {
	host := &fakeHost{verdict: "mismatch", explain: "x"}
	s := New()
	evaluateHosted(s, host, baseLLMArgs())

	forced := baseLLMArgs()
	forced["force"] = "true"
	evaluateHosted(s, host, forced)

	if host.count() != 2 {
		t.Errorf("force should bypass the cache: want 2 calls, got %d", host.count())
	}
}

// Quota exhausted: no model call, verdict error, and the ruleset's error branch
// fires — nothing silently approved.
func TestLLMReviewQuotaExhausted(t *testing.T) {
	const cfg = `{"tenants":[{"name":"acme","api_key":"k","features":["llm_review"],"quota":{"calls_used":5,"calls_limit":5}}]}`
	s := New()
	if err := s.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	host := &fakeHost{verdict: "mismatch", explain: "x"}

	args := baseLLMArgs()
	args[auth.ArgAPIKey] = "k"
	got := decodeEval(t, evaluateHosted(s, host, args))

	if host.count() != 0 {
		t.Errorf("exhausted quota must not call the model, got %d calls", host.count())
	}
	if hasBlock(got.Actions, "pr.merge") {
		t.Error("a quota-exhausted review must not reach the mismatch branch")
	}
	if !hasCommentContaining(got.Actions, "could not run") {
		t.Errorf("the error branch should fire on quota exhaustion, got %+v", got.Actions)
	}
}

// A real call decrements the tenant's live quota.
func TestLLMReviewConsumesQuota(t *testing.T) {
	const cfg = `{"tenants":[{"name":"acme","api_key":"k","features":["llm_review"],"quota":{"calls_used":0,"calls_limit":10}}]}`
	s := New()
	if err := s.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	host := &fakeHost{verdict: "match", explain: "ok"}
	args := baseLLMArgs()
	args[auth.ArgAPIKey] = "k"
	evaluateHosted(s, host, args)

	tenant, _ := s.auth.Authenticate("k")
	if used := s.LLMCallsUsed(tenant); used != 1 {
		t.Errorf("one call should consume one unit of quota, got %d", used)
	}
}

// Standalone (no host): the review degrades to error rather than panicking on a
// nil host, and the error branch fires.
func TestLLMReviewStandaloneDegrades(t *testing.T) {
	s := New()
	s.SetStandalone(true)

	// Unary Execute passes a nil host — the standalone path.
	got := decodeEval(t, s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: baseLLMArgs()}))
	if hasBlock(got.Actions, "pr.merge") {
		t.Error("without a host there is no verdict; the mismatch branch must not fire")
	}
	if !hasCommentContaining(got.Actions, "could not run") {
		t.Errorf("a host-less review should land on the error branch, got %+v", got.Actions)
	}
}

func TestWhoamiWithdrawsFeatureWhenStandalone(t *testing.T) {
	s := configured(t)
	s.SetStandalone(true)
	tenant, _ := s.auth.Authenticate("sekret")
	if !tenant.HasFeature("llm_review") {
		t.Fatal("test precondition: configured tenant should have llm_review")
	}
	for _, f := range s.availableFeatures(tenant) {
		if f == "llm_review" {
			t.Error("standalone mode must withdraw llm_review from advertised features")
		}
	}
}

// plan mode is a dry run: it reports that a review would fire but never calls the
// model.
func TestLLMReviewPlanModeDoesNotSpend(t *testing.T) {
	host := &fakeHost{verdict: "mismatch", explain: "x"}
	args := baseLLMArgs()
	args["mode"] = "plan"
	resp := evaluateHosted(New(), host, args)
	if resp.Error != "" {
		t.Fatalf("plan mode errored: %s", resp.Error)
	}
	if host.count() != 0 {
		t.Errorf("plan mode must not call the model, got %d calls", host.count())
	}
}
