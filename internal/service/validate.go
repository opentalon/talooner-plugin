package service

import (
	"fmt"
	"strings"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// validateRuleset compiles a tenant ruleset with the strict base imported and
// checks its `do` verbs against the closed vocabulary, returning
// {valid, diagnostics}. It powers `talooner rules validate`.
func (s *Server) validateRuleset(req plugin.Request) plugin.Response {
	src := req.Args["ruleset"]
	if strings.TrimSpace(src) == "" {
		resp := &taloonerpb.ValidateRulesetResponse{
			Valid: false,
			Diagnostics: []*taloonerpb.Diagnostic{{
				Severity: taloonerpb.Severity_SEVERITY_ERROR,
				Message:  "ruleset is required",
			}},
		}
		return structuredResponse(req, resp, "invalid: ruleset is required")
	}

	valid, diags := ruleset.Validate(src)

	resp := &taloonerpb.ValidateRulesetResponse{
		Valid:       valid,
		Diagnostics: toProtoDiagnostics(diags),
	}
	summary := fmt.Sprintf("valid=%v diagnostics=%d", valid, len(diags))
	return structuredResponse(req, resp, summary)
}

func toProtoDiagnostics(diags []ruleset.Diagnostic) []*taloonerpb.Diagnostic {
	if len(diags) == 0 {
		return nil
	}
	out := make([]*taloonerpb.Diagnostic, 0, len(diags))
	for _, d := range diags {
		message := d.Message
		if d.Hint != "" {
			// The proto Diagnostic has no hint field; fold it in so the pointer
			// (e.g. to the facts API) reaches the caller intact.
			message += " — " + d.Hint
		}
		out = append(out, &taloonerpb.Diagnostic{
			Severity: toProtoSeverity(d.Severity),
			Message:  message,
			Line:     int32(d.Line),
			Column:   int32(d.Col),
		})
	}
	return out
}

func toProtoSeverity(s ruleset.Severity) taloonerpb.Severity {
	switch s {
	case ruleset.SeverityWarning:
		return taloonerpb.Severity_SEVERITY_WARNING
	case ruleset.SeverityInfo:
		return taloonerpb.Severity_SEVERITY_INFO
	default:
		return taloonerpb.Severity_SEVERITY_ERROR
	}
}
