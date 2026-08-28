package service

import (
	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/internal/llm"
)

// The cache and quota for llm_review. tln's enrich/stale_after cannot express
// either for talooner — the fact store is rebuilt per evaluate_pr, so freshness
// resets every run, and there is no content/sha keying (llm-review.md). So the
// resolver enforces both here: the cache keyed by head sha makes a re-run at an
// unchanged sha free and byte-identical (decisions 9/18); the per-tenant quota
// is the fork-PR spend ceiling.

// --- cache (fact-store-is-the-cache, keyed by head sha) ---

// llmCacheKey identifies a review by (scope, head sha, code unit, prompt
// version). PromptVersion is part of the key so editing the prompt invalidates
// cached verdicts — a new prompt is a new question.
func llmCacheKey(scopeKey, headSHA, unit string) string {
	return scopeKey + "\x00" + headSHA + "\x00" + unit + "\x00" + llm.PromptVersion
}

func (s *Server) llmCacheGet(key string) (llm.Result, bool) {
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	res, ok := s.llmCache[key]
	return res, ok
}

func (s *Server) llmCachePut(key string, res llm.Result) {
	s.llmMu.Lock()
	defer s.llmMu.Unlock()
	s.llmCache[key] = res
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
