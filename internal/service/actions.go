package service

import (
	"fmt"

	"github.com/opentalon/opentalon/pkg/plugin"
)

// registerActions declares the full Talooner action surface (protocol.md,
// "Actions to declare"). Every action is user_only. read_only marks the pure
// queries — is_subscribed, validate_ruleset, explain_pr, whoami — so the host
// skips its per-call confirmation gate; the rest mutate state.
//
// Handlers are stubbed with notImplemented until their owning phase lands; the
// referenced issue is where each body arrives.
func registerActions(s *Server) {
	s.register(plugin.ActionMsg{
		Name:        "evaluate_pr",
		Description: "Compile a ruleset against a PR's facts and return the decision: an abstract action list, an explanation, and warnings. mode=plan returns a plan[] and no executable actions.",
		UserOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: "repo", Description: "Owner/name of the repository", Type: "string", Required: true},
			{Name: "pr", Description: "Pull request number", Type: "string", Required: true},
			{Name: "head_sha", Description: "Head commit SHA under review", Type: "string", Required: true},
			{Name: "facts", Description: "Extracted PR facts, JSON-encoded", Type: "string", Required: true},
			{Name: "ruleset", Description: "Tenant ruleset text", Type: "string", Required: true},
			{Name: "mode", Description: "execute (default) or plan", Type: "string", Required: false},
			{Name: "force", Description: "Bypass the llm_review fact cache for this evaluation (bool)", Type: "string", Required: false},
		},
	}, s.evaluatePR)

	s.register(plugin.ActionMsg{
		Name:        "is_subscribed",
		Description: "Report whether Talooner is subscribed to a PR.",
		UserOnly:    true,
		ReadOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: "repo", Description: "Owner/name of the repository", Type: "string", Required: true},
			{Name: "pr", Description: "Pull request number", Type: "string", Required: true},
		},
	}, s.isSubscribed)

	s.register(plugin.ActionMsg{
		Name:        "set_subscription",
		Description: "Set Talooner's subscription state for a PR.",
		UserOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: "repo", Description: "Owner/name of the repository", Type: "string", Required: true},
			{Name: "pr", Description: "Pull request number", Type: "string", Required: true},
			{Name: "state", Description: "Desired subscription state (bool)", Type: "string", Required: true},
		},
	}, s.setSubscription)

	s.register(plugin.ActionMsg{
		Name:        "assert_facts",
		Description: "Assert custom facts for a PR after namespace enforcement. Store-only in v1: validates and persists, returns which facts were accepted or rejected, and returns no actions.",
		UserOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: "repo", Description: "Owner/name of the repository", Type: "string", Required: true},
			{Name: "pr", Description: "Pull request number", Type: "string", Required: true},
			{Name: "facts", Description: "Facts to assert, JSON-encoded", Type: "string", Required: true},
		},
	}, notImplemented("P-C4"))

	s.register(plugin.ActionMsg{
		Name:        "validate_ruleset",
		Description: "Parse, validate and compile a ruleset, returning diagnostics. Powers `talooner rules validate`.",
		UserOnly:    true,
		ReadOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: "ruleset", Description: "Ruleset text to validate", Type: "string", Required: true},
		},
	}, s.validateRuleset)

	s.register(plugin.ActionMsg{
		Name:        "explain_pr",
		Description: "Return the recorded explanation for a PR's last decision. Powers `@talooner /why`.",
		UserOnly:    true,
		ReadOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: "repo", Description: "Owner/name of the repository", Type: "string", Required: true},
			{Name: "pr", Description: "Pull request number", Type: "string", Required: true},
			{Name: "head_sha", Description: "Head commit SHA to explain", Type: "string", Required: true},
		},
	}, notImplemented("P-C5"))

	s.register(plugin.ActionMsg{
		Name:        "whoami",
		Description: "Capability handshake: report tenant, quota, available models, features and the plugin's protocol version. The caller uses it to fail fast on version skew and to know whether llm_review is available.",
		UserOnly:    true,
		ReadOnly:    true,
		Parameters: []plugin.ParameterMsg{
			{Name: ArgProtocolVersion, Description: "Caller's protocol version; a below-floor value is rejected", Type: "string", Required: false},
		},
	}, s.whoami)
}

// notImplemented is a placeholder handler for an action whose body has not
// landed yet. issue names the phase that fills it in.
func notImplemented(issue string) HandlerFunc {
	return func(req plugin.Request) plugin.Response {
		return plugin.Response{
			CallID: req.ID,
			Error:  fmt.Sprintf("talooner: action %q not implemented yet (%s)", req.Action, issue),
		}
	}
}
