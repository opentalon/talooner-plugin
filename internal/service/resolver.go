package service

import (
	"context"
	"fmt"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/internal/llm"
)

// reviewServer / reviewTool are the (server, tool) a ruleset names in a
// `tool "llm" "review" { ... }` step. The resolver intercepts exactly this pair.
const (
	reviewServer = "llm"
	reviewTool   = "review"
)

// reviewResolver implements tln.ToolResolver (Call). It is the engine-native
// entry point for llm_review: an `enrich` block's `tool "llm" "review"` step
// calls it once per matching code_unit row, and it turns that into a bounded
// host sub-agent call. The head-sha cache and per-tenant quota are enforced here
// because tln's stale_after cannot express either for talooner's rebuilt-per-run
// fact store (llm-review.md).
//
// Call never returns a Go error for the review path: every failure maps to a
// verdict ("error"/"too_large"/"unclear") so enrich still asserts a fact and a
// ruleset can react — a returned error would make enrich skip the row, leaving
// the result unset, which reads as "no problem" under the A1 semantics.
type reviewResolver struct {
	srv      *Server
	host     plugin.HostCaller // may be nil (standalone/no host) → verdict "error"
	tenant   auth.Tenant
	scopeKey string
	headSHA  string
	force    bool
}

func (r *reviewResolver) Call(_ context.Context, server, tool string, args map[string]any) (any, error) {
	if server != reviewServer || tool != reviewTool {
		return nil, fmt.Errorf("talooner: unknown tool %q %q", server, tool)
	}

	ctx := context.Background()
	unit := argString(args, "unit")
	in := llm.ReviewInput{
		Unit:          unit,
		DocContent:    argString(args, "doc"),
		Diff:          argString(args, "diff"),
		DiffTruncated: argBool(args, "diff_truncated"),
	}

	key := llmCacheKey(r.scopeKey, r.headSHA, unit)

	// The fact store is the cache: a hit at the same head sha replays the verdict
	// with no call and no spend. force bypasses the hit but not the quota below.
	if !r.force {
		if res, ok := r.srv.llmCacheGet(key); ok {
			return resultMap(res), nil
		}
	}

	// Per-tenant ceiling, checked before spending. force does not lift it.
	if !r.srv.llmQuotaAvailable(r.tenant) {
		return resultMap(llm.Result{
			Verdict:     llm.VerdictError,
			Explanation: "llm_review quota exhausted for this tenant; no call was made",
		}), nil
	}

	res := llm.Review(ctx, r.host, in)
	switch res.Verdict {
	case llm.VerdictMatch, llm.VerdictMismatch, llm.VerdictUnclear:
		// A real model call happened: consume quota and cache so a re-run at this
		// sha is free and deterministic.
		r.srv.llmQuotaConsume(r.tenant)
		r.srv.llmCachePut(key, res)
	case llm.VerdictTooLarge:
		// No model call, but deterministic at this sha — cache it, no quota spent.
		r.srv.llmCachePut(key, res)
	case llm.VerdictError:
		// Transient (host absent or call failed): do not cache, do not consume, so
		// the next run can retry.
	}
	return resultMap(res), nil
}

// resultMap is the shape an enrich block reads via `update attr X from
// result.verdict` / `result.explanation`.
func resultMap(res llm.Result) map[string]any {
	return map[string]any{
		"verdict":     string(res.Verdict),
		"explanation": res.Explanation,
	}
}

func argString(args map[string]any, k string) string {
	if v, ok := args[k].(string); ok {
		return v
	}
	return ""
}

func argBool(args map[string]any, k string) bool {
	switch v := args[k].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}
