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

// generateRuleset scaffolds a rules.tln + rules.tln.test pair for a repo,
// powering `talooner onboard`. It mirrors llm_review's host call
// (internal/llm/generate.go, internal/service/resolver.go) rather than a new
// pattern: same host.RunAction path, same per-tenant quota. It then
// self-verifies the model's output through the same compile/test path
// validate_ruleset and run_ruleset_test use, and falls back — empty
// ruleset/ruleset_test, source="fallback", note explaining why — on any
// failure: no host (standalone), quota exhausted, an unparseable model
// reply, or a generated pair that doesn't compile or doesn't pass its own
// tests. The plugin never embeds a starter ruleset of its own; the caller
// (talooner onboard) supplies its own known-good starter on fallback.
func (s *Server) generateRuleset(ctx context.Context, req plugin.Request, host plugin.HostCaller) plugin.Response {
	summary := req.Args["repo_summary"]
	if strings.TrimSpace(summary) == "" {
		return errorResponse(req, fmt.Errorf("talooner: generate_ruleset needs a repo_summary"))
	}

	tenant, _ := s.auth.Authenticate(req.Args[auth.ArgAPIKey])

	if host == nil {
		return generateFallback(req, "generate_ruleset is unavailable: this plugin is running without an OpenTalon host to perform the call")
	}
	if !s.llmQuotaAvailable(tenant) {
		return generateFallback(req, "generate_ruleset is unavailable: the tenant's LLM call budget is exhausted")
	}

	src, testSrc, ok, explanation := llm.Generate(ctx, host, llm.GenerateInput{RepoSummary: summary})
	if !ok {
		// Transient (host error or unparseable reply): do not consume quota, so
		// a retry isn't penalized for a call that produced nothing usable —
		// same convention as reviewResolver's VerdictError branch.
		return generateFallback(req, explanation)
	}
	s.llmQuotaConsume(tenant)

	if valid, diags := ruleset.Validate(src); !valid {
		return generateFallback(req, "generated ruleset did not compile: "+firstDiagnosticMessage(diags))
	}
	results, diags, err := ruleset.RunTests(src, testSrc)
	if err != nil {
		return generateFallback(req, "generated test source did not compile: "+firstDiagnosticMessage(diags))
	}
	for _, r := range results {
		if !r.Passed {
			return generateFallback(req, fmt.Sprintf("generated ruleset failed its own test %q", r.Name))
		}
	}

	resp := &taloonerpb.GenerateRulesetResponse{
		Ruleset:     src,
		RulesetTest: testSrc,
		Source:      "llm",
	}
	return structuredResponse(req, resp, fmt.Sprintf("generated and verified a %d-test ruleset", len(results)))
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
