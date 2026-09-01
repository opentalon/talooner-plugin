package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/internal/llm"
	"github.com/opentalon/talooner-plugin/internal/ruleset"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// maxGenerateAttempts bounds how many times generateRuleset will show the
// model its own compiler/test failure and ask it to fix it. Each attempt is
// a real subprocess LLM call (quota + ~10-20s wall clock), so this trades a
// slower onboard for a working ruleset landing in the PR instead of a
// generic starter — 3 gives the model two chances to self-correct before
// giving up.
const maxGenerateAttempts = 3

// generateRuleset scaffolds a rules.tln + rules.tln.test pair for a repo,
// powering `talooner onboard`. It mirrors llm_review's host call
// (internal/llm/generate.go, internal/service/resolver.go) rather than a new
// pattern: same host.RunAction path, same per-tenant quota. It then
// self-verifies the model's output through the same compile/test path
// validate_ruleset and run_ruleset_test use; on a compile or test failure it
// feeds the diagnostic back to the model and retries, up to
// maxGenerateAttempts, before falling back — empty ruleset/ruleset_test,
// source="fallback", note explaining why. A no-host, quota-exhausted, or
// unparseable-reply failure falls back immediately without retrying, since
// none of those are something a fix-up prompt can address. The plugin never
// embeds a starter ruleset of its own; the caller (talooner onboard)
// supplies its own known-good starter on fallback.
func (s *Server) generateRuleset(ctx context.Context, req plugin.Request, host plugin.HostCaller) plugin.Response {
	summary := req.Args["repo_summary"]
	if strings.TrimSpace(summary) == "" {
		return errorResponse(req, fmt.Errorf("talooner: generate_ruleset needs a repo_summary"))
	}

	tenant, _ := s.auth.Authenticate(req.Args[auth.ArgAPIKey])

	if host == nil {
		return generateFallback(req, "generate_ruleset is unavailable: this plugin is running without an OpenTalon host to perform the call")
	}

	var prior *llm.PriorAttempt
	var lastNote string
	for attempt := 1; attempt <= maxGenerateAttempts; attempt++ {
		if !s.llmQuotaAvailable(tenant) {
			return generateFallback(req, "generate_ruleset is unavailable: the tenant's LLM call budget is exhausted")
		}

		src, testSrc, ok, explanation := llm.Generate(ctx, host, llm.GenerateInput{RepoSummary: summary, Prior: prior})
		if !ok {
			// Transient (host error or unparseable reply): do not consume quota,
			// and don't retry — a broken call/parse isn't something a fix-up
			// prompt can address, same convention as reviewResolver's
			// VerdictError branch.
			return generateFallback(req, explanation)
		}
		s.llmQuotaConsume(tenant)

		if valid, diags := ruleset.Validate(src); !valid {
			lastNote = "generated ruleset did not compile: " + firstDiagnosticMessage(diags)
			prior = &llm.PriorAttempt{Ruleset: src, RulesetTest: testSrc, Error: lastNote}
			continue
		}
		results, diags, err := ruleset.RunTests(src, testSrc)
		if err != nil {
			lastNote = "generated test source did not compile: " + firstDiagnosticMessage(diags)
			prior = &llm.PriorAttempt{Ruleset: src, RulesetTest: testSrc, Error: lastNote}
			continue
		}
		failed := false
		for _, r := range results {
			if !r.Passed {
				lastNote = fmt.Sprintf("generated ruleset failed its own test %q", r.Name)
				failed = true
				break
			}
		}
		if failed {
			prior = &llm.PriorAttempt{Ruleset: src, RulesetTest: testSrc, Error: lastNote}
			continue
		}

		resp := &taloonerpb.GenerateRulesetResponse{
			Ruleset:     src,
			RulesetTest: testSrc,
			Source:      "llm",
		}
		return structuredResponse(req, resp, fmt.Sprintf("generated and verified a %d-test ruleset", len(results)))
	}

	return generateFallback(req, fmt.Sprintf("model output still failed verification after %d attempts: %s", maxGenerateAttempts, lastNote))
}

func generateFallback(req plugin.Request, note string) plugin.Response {
	resp := &taloonerpb.GenerateRulesetResponse{Source: "fallback", Note: note}
	return structuredResponse(req, resp, "fallback: "+note)
}

func firstDiagnosticMessage(diags []ruleset.Diagnostic) string {
	if len(diags) == 0 {
		return "no diagnostics reported"
	}
	return diags[0].Message
}
