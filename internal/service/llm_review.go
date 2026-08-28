package service

import (
	"context"
	"fmt"

	"github.com/opentalon/opentalon/pkg/plugin"
	"github.com/opentalon/tln-language/pkg/tln"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/internal/llm"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// llm_review.* fact attributes the plugin writes for the second engine pass. The
// namespace is reserved from tenant writes (facts.md), so these are the plugin's
// alone. The enum drives decisions; explanation is rendered only as quoted text.
const (
	factLLMResult      = "llm_review.result"
	factLLMExplanation = "llm_review.explanation"
	factLLMError       = "llm_review.error"
	factLLMDocURL      = "llm_review.doc_url"
)

// resolveLLMReviews performs the fired llm_review actions and returns the
// llm_review.* facts to assert for the second pass. It never returns an error:
// every failure (quota, host, oversize) becomes an llm_review.result value so a
// ruleset can react and nothing silently approves (llm-review.md).
//
// v1 evaluates one review per PR (facts.md, "one evaluation per PR"); if a
// ruleset fires more than one, the first is reviewed and the rest warned about.
func (s *Server) resolveLLMReviews(
	ctx context.Context,
	host plugin.HostCaller,
	req plugin.Request,
	scopeKey string,
	state facts.Set,
	reviews []tln.Action,
) (facts.Set, []*taloonerpb.Warning) {
	var warnings []*taloonerpb.Warning

	force := req.Args["force"] == "true"
	headSHA := req.Args["head_sha"]
	// Zero-value tenant when unconfigured (tests/dev): quota is then unlimited,
	// which matches the ungated dispatch path.
	tenant, _ := s.auth.Authenticate(req.Args[auth.ArgAPIKey])

	docURL := argAt(reviews[0].Args, 0)
	if len(reviews) > 1 {
		warnings = append(warnings, &taloonerpb.Warning{
			Code:    "llm_review_multiple",
			Message: fmt.Sprintf("%d llm_review actions fired; v1 evaluates one per PR, using %q", len(reviews), docURL),
		})
	}

	key := llmCacheKey(scopeKey, headSHA, docURL)

	// The fact store is the cache: a hit at the same head sha replays the verdict
	// with no call and no spend. force bypasses the hit but not the caps below.
	if !force {
		if set, ok := s.llmCacheGet(key); ok {
			return set, warnings
		}
	}

	// Per-tenant ceiling, checked before spending. Exhaustion asserts error and
	// returns — force does not lift it (protocol.md, "force"). The per-PR call
	// cap is inherently 1 in v1: llm_review.* facts are flat, so a PR carries one
	// verdict and we review one doc (the first fired) per evaluation.
	if !s.llmQuotaAvailable(tenant) {
		return llmErrorSet(docURL, "llm_review quota exhausted for this tenant; no call was made"), warnings
	}

	in := llm.ReviewInput{
		DocURL:        docURL,
		Title:         factString(state, "pr.title"),
		Body:          factString(state, "pr.body"),
		Diff:          factString(state, "pr.diff"),
		HeadSHA:       headSHA,
		DiffTruncated: factBool(state, "pr.diff_truncated"),
	}
	result := llm.Review(ctx, host, in)
	set := llmResultSet(docURL, result)

	switch result.Verdict {
	case llm.VerdictMatch, llm.VerdictMismatch, llm.VerdictUnclear:
		// A real model call happened: consume quota and cache the verdict so a
		// re-run at this sha is free and deterministic (decision 9/18).
		s.llmQuotaConsume(tenant)
		s.llmCachePut(key, set)
	case llm.VerdictTooLarge:
		// No model call, but deterministic at this sha — cache it so /review is
		// free and no quota is spent.
		s.llmCachePut(key, set)
	case llm.VerdictError:
		// Transient (host absent or call failed): do not cache and do not
		// consume quota, so the next run can retry.
	}
	return set, warnings
}

// llmResultSet renders a review outcome as llm_review.* facts.
func llmResultSet(docURL string, r llm.Result) facts.Set {
	set := facts.Set{
		factLLMResult: string(r.Verdict),
		factLLMDocURL: docURL,
	}
	if r.Verdict == llm.VerdictError {
		set[factLLMError] = r.Explanation
	} else {
		set[factLLMExplanation] = r.Explanation
	}
	return set
}

// llmErrorSet is the fact set for a review that never reached the model.
func llmErrorSet(docURL, msg string) facts.Set {
	return facts.Set{
		factLLMResult: string(llm.VerdictError),
		factLLMDocURL: docURL,
		factLLMError:  msg,
	}
}

// --- quota (per-tenant, persisted) ---

// llmQuotaAvailable reports whether the tenant has llm_review budget left. A
// non-positive CallsLimit means unlimited (unconfigured tenant or no ceiling
// set). The live counter is seeded from config's CallsUsed the first time.
func (s *Server) llmQuotaAvailable(tenant auth.Tenant) bool {
	if tenant.Quota.CallsLimit <= 0 {
		return true
	}
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	used, ok := s.llmQuota[tenant.Name]
	if !ok {
		used = tenant.Quota.CallsUsed
		s.llmQuota[tenant.Name] = used
	}
	return used < tenant.Quota.CallsLimit
}

// llmQuotaConsume records one call against the tenant's live counter.
func (s *Server) llmQuotaConsume(tenant auth.Tenant) {
	if tenant.Name == "" {
		return // unconfigured/anonymous: nothing to meter
	}
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	used, ok := s.llmQuota[tenant.Name]
	if !ok {
		used = tenant.Quota.CallsUsed
	}
	s.llmQuota[tenant.Name] = used + 1
}

// LLMCallsUsed returns the tenant's live llm_review call count, seeded from
// config. whoami surfaces it so a caller sees remaining budget (llm-review.md).
func (s *Server) LLMCallsUsed(tenant auth.Tenant) int64 {
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	if used, ok := s.llmQuota[tenant.Name]; ok {
		return used
	}
	return tenant.Quota.CallsUsed
}

// --- cache (fact-store-is-the-cache) ---

// llmCacheKey identifies a review by (scope, head sha, doc, prompt version).
// PromptVersion is part of the key so editing the prompt invalidates cached
// verdicts — a new prompt is a new question (llm-review.md).
func llmCacheKey(scopeKey, headSHA, docURL string) string {
	return scopeKey + "\x00" + headSHA + "\x00" + docURL + "\x00" + llm.PromptVersion
}

func (s *Server) llmCacheGet(key string) (facts.Set, bool) {
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	set, ok := s.llmCache[key]
	if !ok {
		return nil, false
	}
	return cloneSet(set), true
}

func (s *Server) llmCachePut(key string, set facts.Set) {
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	s.llmCache[key] = cloneSet(set)
}

// --- small helpers ---

func cloneSet(set facts.Set) facts.Set {
	out := make(facts.Set, len(set))
	for k, v := range set {
		out[k] = v
	}
	return out
}

func factString(set facts.Set, key string) string {
	if v, ok := set[key].(string); ok {
		return v
	}
	return ""
}

func factBool(set facts.Set, key string) bool {
	if v, ok := set[key].(bool); ok {
		return v
	}
	return false
}
