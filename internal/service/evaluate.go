package service

import (
	"context"
	"fmt"
	"strconv"

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
func (s *Server) evaluatePR(req plugin.Request) plugin.Response {
	switch mode := req.Args["mode"]; mode {
	case "", "execute":
		// execute mode
	case "plan":
		return errorResponse(req, fmt.Errorf("talooner: mode \"plan\" is not implemented yet (P-C3)"))
	default:
		return errorResponse(req, fmt.Errorf("talooner: unknown mode %q; expected \"execute\" or \"plan\"", mode))
	}

	repo := req.Args["repo"]
	prNumber, err := strconv.Atoi(req.Args["pr"])
	if err != nil {
		return errorResponse(req, fmt.Errorf("talooner: invalid pr %q: must be a number", req.Args["pr"]))
	}

	set, err := facts.Decode(req.Args["facts"])
	if err != nil {
		return errorResponse(req, err) // names the offending attribute
	}

	ctx := context.Background()

	// Full re-derivation. There is no persisted prior scope yet (talon-db
	// persistence lands later), so the request is the complete fact set.
	key := facts.Key(repo, prNumber)
	state := facts.Rederive(nil, set)
	// Subscription is a fact: surface it so `attr "pr.subscribed"` is readable
	// in rules. The plugin owns it (set via set_subscription), so it is injected
	// here rather than taken from the bot's request.
	state["pr.subscribed"] = s.subscribedFor(key)

	scope := facts.NewScope(key)
	if err := scope.Assert(ctx, state); err != nil {
		return errorResponse(req, fmt.Errorf("talooner: assert facts: %w", err))
	}

	result, err := ruleset.Evaluate(ctx, req.Args["ruleset"], scope.Store())
	if err != nil {
		return errorResponse(req, fmt.Errorf("talooner: evaluate ruleset: %w", err))
	}

	actions, warnings := mapActions(result.Actions)
	resp := &taloonerpb.EvaluatePrResponse{
		Actions:  actions,
		Explain:  buildExplain(result.Actions),
		Warnings: warnings,
	}
	summary := fmt.Sprintf("%s#%d: %d action(s), %d warning(s)", repo, prNumber, len(actions), len(warnings))
	return structuredResponse(req, resp, summary)
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

// buildExplain summarises the decision. A ruleset that compiled and evaluated
// but fired nothing yields an explanation that says so — never an empty
// response that reads as "not evaluated".
func buildExplain(fired []talon.Action) *taloonerpb.Explain {
	rules := make([]string, 0)
	seen := map[string]bool{}
	for _, a := range fired {
		if !seen[a.Rule] {
			seen[a.Rule] = true
			rules = append(rules, a.Rule)
		}
	}

	if len(rules) == 0 {
		return &taloonerpb.Explain{
			Summary: "no rules fired: the ruleset compiled and evaluated, but no rule matched the facts",
		}
	}

	firings := make([]*taloonerpb.RuleFiring, 0, len(rules))
	for _, r := range rules {
		firings = append(firings, &taloonerpb.RuleFiring{Rule: r})
	}
	return &taloonerpb.Explain{
		Summary: fmt.Sprintf("%d rule(s) fired", len(rules)),
		Firings: firings,
	}
}
