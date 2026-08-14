package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/opentalon/opentalon/pkg/plugin"
	"github.com/opentalon/talon-language/pkg/talon"

	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/internal/ruleset"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// evaluatePR is the central action: it turns a PR's facts and a ruleset into an
// abstract action list. Pipeline (engine.md): decode facts → re-derive into the
// PR scope → compile the ruleset with the strict base → run the engine → map
// fired actions → {actions, explain, warnings}.
//
// Actions come back as data with arguments already resolved per matched row
// (`do assign "pr" attr "user.owner"` arrives carrying the owner value); the
// plugin does not know what any verb means on GitHub.
//
// mode selects execute (default) or plan. In plan mode the decision is a dry
// run — used for a head-branch ruleset on a fork PR: the actions that WOULD fire
// are returned in the distinct `plan` field, never `actions`, and nothing is
// persisted. The distinction lives in the payload shape, so a caller cannot
// execute a plan by accident.
func (s *Server) evaluatePR(req plugin.Request) plugin.Response {
	mode := req.Args["mode"]
	switch mode {
	case "", "execute", "plan":
		// ok
	default:
		return errorResponse(req, fmt.Errorf("talooner: unknown mode %q; expected \"execute\" or \"plan\"", mode))
	}
	planMode := mode == "plan"

	repo := req.Args["repo"]
	prNumber, err := strconv.Atoi(req.Args["pr"])
	if err != nil {
		return errorResponse(req, fmt.Errorf("talooner: invalid pr %q: must be a number", req.Args["pr"]))
	}

	set, err := facts.Decode(req.Args["facts"])
	if err != nil {
		return errorResponse(req, err) // names the offending attribute
	}

	mods, err := parseModules(req.Args["modules"])
	if err != nil {
		return errorResponse(req, err)
	}

	ctx := context.Background()

	// Full re-derivation over the durable prior: custom tenant-CI facts asserted
	// via assert_facts survive (they are not bot-owned), and the request's bot
	// facts replace the previous ones. This is how an out-of-band assert_facts
	// reaches a verdict at the next evaluate_pr.
	key := facts.Key(repo, prNumber)
	state := facts.Rederive(s.tenantFactsFor(key), set)
	// Subscription is a fact: surface it so `attr "pr.subscribed"` is readable
	// in rules. The plugin owns it (set via set_subscription), so it is injected
	// here rather than taken from the bot's request.
	state["pr.subscribed"] = s.subscribedFor(key)

	// Bind module.* to the primary touched module (one evaluation per PR).
	injectModuleFacts(state, mods)

	scope := facts.NewScope(key)
	if err := scope.Assert(ctx, state); err != nil {
		return errorResponse(req, fmt.Errorf("talooner: assert facts: %w", err))
	}

	tenantRuleset := req.Args["ruleset"]
	result, err := ruleset.Evaluate(ctx, tenantRuleset, scope.Store())
	if err != nil {
		return errorResponse(req, fmt.Errorf("talooner: evaluate ruleset: %w", err))
	}

	// Defeasible resolution. The engine has already settled `overrides`; this
	// applies the remaining strict > priority precedence to any standing
	// approve/block conflict, dropping the defeated side's actions or warning on
	// an unresolved tie (P-C1).
	resolved, conflictWarnings := resolveConflicts(result.Actions, ruleset.RuleMeta(tenantRuleset))

	actions, warnings := mapActions(resolved)
	for _, w := range conflictWarnings {
		warnings = append(warnings, &taloonerpb.Warning{Code: "unresolved_conflict", Message: w})
	}
	fired := firedRuleNames(resolved)
	explain := buildExplain(fired)

	// Plan mode is a dry run: return the actions that would fire in the distinct
	// `plan` field (never `actions`) and persist nothing. The payload shape is
	// what makes a plan unexecutable, not a convention.
	if planMode {
		resp := &taloonerpb.EvaluatePrResponse{
			Plan:     actions,
			Explain:  explain,
			Warnings: warnings,
		}
		summary := fmt.Sprintf("%s#%d: plan — %d action(s) would fire, %d warning(s)", repo, prNumber, len(actions), len(warnings))
		return structuredResponse(req, resp, summary)
	}

	// Persist the decision BEFORE the response leaves. The caller is a workflow
	// run that can be cancelled mid-flight, so if the record were written after
	// the response, the most common failure mode would be the one with no audit
	// trail.
	s.persistDecision(Decision{
		Repo:        repo,
		PR:          prNumber,
		HeadSHA:     req.Args["head_sha"],
		RulesetHash: ruleset.Hash(tenantRuleset),
		Facts:       state,
		Fired:       fired,
		NotFired:    subtract(ruleset.RuleNames(tenantRuleset), fired),
		Actions:     actions,
		Explain:     explain,
		At:          time.Now().Unix(),
	})

	resp := &taloonerpb.EvaluatePrResponse{
		Actions:  actions,
		Explain:  explain,
		Warnings: warnings,
	}
	summary := fmt.Sprintf("%s#%d: %d action(s), %d warning(s)", repo, prNumber, len(actions), len(warnings))
	return structuredResponse(req, resp, summary)
}

// firedRuleNames returns the distinct rule names that fired, in first-seen order.
func firedRuleNames(fired []talon.Action) []string {
	seen := map[string]bool{}
	var names []string
	for _, a := range fired {
		if !seen[a.Rule] {
			seen[a.Rule] = true
			names = append(names, a.Rule)
		}
	}
	return names
}

// subtract returns the elements of all not present in remove.
func subtract(all, remove []string) []string {
	drop := make(map[string]bool, len(remove))
	for _, r := range remove {
		drop[r] = true
	}
	var out []string
	for _, a := range all {
		if !drop[a] {
			out = append(out, a)
		}
	}
	return out
}

// mapActions converts fired engine actions to the contract's action list. A verb
// outside the vocabulary is dropped and surfaced as a warning rather than
// reaching the bot as an action it cannot execute (engine.md).
func mapActions(fired []talon.Action) ([]*taloonerpb.Action, []*taloonerpb.Warning) {
	var actions []*taloonerpb.Action
	var warnings []*taloonerpb.Warning
	for _, a := range fired {
		pa, ok := toProtoAction(a)
		if !ok {
			warnings = append(warnings, &taloonerpb.Warning{
				Code:    "unknown_verb",
				Message: fmt.Sprintf("rule %q fired verb %q, which is outside the action vocabulary; dropped", a.Rule, a.Verb),
			})
			continue
		}
		actions = append(actions, pa)
	}
	return actions, warnings
}

func toProtoAction(a talon.Action) (*taloonerpb.Action, bool) {
	pa := &taloonerpb.Action{}
	switch a.Verb {
	case "approve":
		pa.Verb, pa.Target = taloonerpb.Verb_VERB_APPROVE, argAt(a.Args, 0)
	case "block":
		pa.Verb, pa.Target = taloonerpb.Verb_VERB_BLOCK, argAt(a.Args, 0)
	case "comment":
		pa.Verb, pa.Target, pa.Text = taloonerpb.Verb_VERB_COMMENT, argAt(a.Args, 0), argAt(a.Args, 1)
	case "assign":
		pa.Verb, pa.Target, pa.Assignee = taloonerpb.Verb_VERB_ASSIGN, argAt(a.Args, 0), argAt(a.Args, 1)
	case "require":
		pa.Verb, pa.Target = taloonerpb.Verb_VERB_REQUIRE, argAt(a.Args, 0)
	case "notify":
		pa.Verb, pa.Target, pa.Text = taloonerpb.Verb_VERB_NOTIFY, argAt(a.Args, 0), argAt(a.Args, 1)
	case "emit":
		pa.Verb, pa.Name = taloonerpb.Verb_VERB_EMIT, argAt(a.Args, 0)
	default:
		return nil, false
	}
	return pa, true
}

// argAt returns the i-th resolved argument as a string. Arguments arrive already
// resolved against the matched row, so a value may be any scalar; Sprint keeps
// it faithful without assuming a type.
func argAt(args []any, i int) string {
	if i < 0 || i >= len(args) {
		return ""
	}
	return fmt.Sprint(args[i])
}

// buildExplain summarises the decision from the fired rule names. A ruleset
// that compiled and evaluated but fired nothing yields an explanation that says
// so — never an empty response that reads as "not evaluated".
func buildExplain(fired []string) *taloonerpb.Explain {
	if len(fired) == 0 {
		return &taloonerpb.Explain{
			Summary: "no rules fired: the ruleset compiled and evaluated, but no rule matched the facts",
		}
	}
	firings := make([]*taloonerpb.RuleFiring, 0, len(fired))
	for _, r := range fired {
		firings = append(firings, &taloonerpb.RuleFiring{Rule: r})
	}
	return &taloonerpb.Explain{
		Summary: fmt.Sprintf("%d rule(s) fired", len(fired)),
		Firings: firings,
	}
}
