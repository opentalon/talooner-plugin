package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/opentalon/talon-language/pkg/talon"

	"github.com/opentalon/talooner-plugin/internal/ruleset"
)

// resolveConflicts settles an approve/block conflict left standing after the
// engine's `overrides` resolution, applying the remaining defeasible
// precedence: a strict rule defeats a non-strict one, and among equal
// strictness a higher priority defeats a lower (P-C1). The engine does not
// auto-defeat on strict/priority without an overrides edge, so this layer adds
// it — using the rules' *declared* strict/priority, not an ad-hoc block-wins.
//
// A defeated side's rules have all their actions dropped, matching the engine's
// overrides semantics. An unresolved tie (equal strictness and priority) returns
// both and warns: the warning is the product, so the maintainer disambiguates
// with `overrides` or `priority`; the bot applies block-wins only as a
// last-resort tiebreak.
func resolveConflicts(fired []talon.Action, meta map[string]ruleset.Meta) (kept []talon.Action, warnings []string) {
	approvers := sideRules(fired, "approve")
	blockers := sideRules(fired, "block")
	if len(approvers) == 0 || len(blockers) == 0 {
		return fired, nil // no approve/block conflict to resolve
	}

	switch cmp := compareStrength(bestStrength(approvers, meta), bestStrength(blockers, meta)); {
	case cmp > 0:
		return drop(fired, blockers), nil
	case cmp < 0:
		return drop(fired, approvers), nil
	default:
		return fired, []string{tieWarning(approvers, blockers)}
	}
}

// sideRules returns the set of rule names that fired a given verb.
func sideRules(fired []talon.Action, verb string) map[string]bool {
	out := map[string]bool{}
	for _, a := range fired {
		if a.Verb == verb {
			out[a.Rule] = true
		}
	}
	return out
}

type strength struct {
	strict   bool
	priority int
}

// bestStrength is the strongest rule on a side: strict outranks non-strict, then
// higher priority outranks lower.
func bestStrength(rules map[string]bool, meta map[string]ruleset.Meta) strength {
	best := strength{priority: ruleset.PriorityLow - 1}
	for name := range rules {
		m, ok := meta[name]
		if !ok {
			m = ruleset.Meta{Priority: ruleset.PriorityMedium}
		}
		s := strength{strict: m.Strict, priority: m.Priority}
		if compareStrength(s, best) > 0 {
			best = s
		}
	}
	return best
}

// compareStrength returns >0 if a beats b, <0 if b beats a, 0 on a tie.
func compareStrength(a, b strength) int {
	if a.strict != b.strict {
		if a.strict {
			return 1
		}
		return -1
	}
	return a.priority - b.priority
}

// drop removes every action belonging to a losing rule.
func drop(fired []talon.Action, losers map[string]bool) []talon.Action {
	kept := make([]talon.Action, 0, len(fired))
	for _, a := range fired {
		if losers[a.Rule] {
			continue
		}
		kept = append(kept, a)
	}
	return kept
}

func tieWarning(approvers, blockers map[string]bool) string {
	return fmt.Sprintf(
		"unresolved conflict: approve (%s) and block (%s) fired at equal precedence for this PR; "+
			"disambiguate with `overrides` or `priority`. Both are returned; the bot applies block-wins as a last resort.",
		strings.Join(sortedKeys(approvers), ", "), strings.Join(sortedKeys(blockers), ", "))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
