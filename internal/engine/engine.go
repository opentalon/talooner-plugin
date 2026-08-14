// Package engine wraps the Talon language SDK the plugin compiles and runs
// rulesets with. Phase 1 (P-A1) only needs to prove the dependency chain
// links; the real validate/evaluate surface arrives with the ruleset loader
// (P-B2) and evaluate_pr (P-B7).
package engine

import "github.com/opentalon/talon-language/pkg/talon"

// ValidateSource compiles a Talon ruleset and returns any compile error. It is
// a thin pass-through over the Talon SDK's Check, and exists so the plugin's
// build links talon-language and, transitively, talon-db — the dependency
// chain P-A1 is here to establish.
func ValidateSource(src string) error {
	return talon.Check(src)
}
