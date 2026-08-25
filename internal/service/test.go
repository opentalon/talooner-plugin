package service

import (
	"fmt"
	"strings"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// runRulesetTest compiles a tenant ruleset with the strict base imported and
// runs a paired .tln.test source against it, returning pass/fail per test
// block. It powers `talooner rules test`.
func (s *Server) runRulesetTest(req plugin.Request) plugin.Response {
	src := req.Args["ruleset"]
	if strings.TrimSpace(src) == "" {
		resp := &taloonerpb.RunRulesetTestResponse{
			Diagnostics: []*taloonerpb.Diagnostic{{
				Severity: taloonerpb.Severity_SEVERITY_ERROR,
				Message:  "ruleset is required",
			}},
		}
		return structuredResponse(req, resp, "invalid: ruleset is required")
	}
	testSrc := req.Args["test_source"]
	if strings.TrimSpace(testSrc) == "" {
		resp := &taloonerpb.RunRulesetTestResponse{
			Diagnostics: []*taloonerpb.Diagnostic{{
				Severity: taloonerpb.Severity_SEVERITY_ERROR,
				Message:  "test_source is required",
			}},
		}
		return structuredResponse(req, resp, "invalid: test_source is required")
	}

	results, diags, err := ruleset.RunTests(src, testSrc)
	if err != nil {
		resp := &taloonerpb.RunRulesetTestResponse{
			Diagnostics: toProtoDiagnostics(diags),
		}
		summary := fmt.Sprintf("compile failed: diagnostics=%d", len(diags))
		return structuredResponse(req, resp, summary)
	}

	resp := &taloonerpb.RunRulesetTestResponse{
		Results: toProtoOutcomes(results),
	}
	passed := 0
	for _, r := range results {
		if r.Passed {
			passed++
		}
	}
	summary := fmt.Sprintf("passed=%d/%d", passed, len(results))
	return structuredResponse(req, resp, summary)
}

func toProtoOutcomes(results []ruleset.TestResult) []*taloonerpb.TestOutcome {
	if len(results) == 0 {
		return nil
	}
	out := make([]*taloonerpb.TestOutcome, 0, len(results))
	for _, r := range results {
		out = append(out, &taloonerpb.TestOutcome{
			Name:   r.Name,
			Passed: r.Passed,
			Errors: r.Errors,
		})
	}
	return out
}
