package service

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// parseModules decodes the evaluate_pr `modules` arg — the touched modules the
// bot resolved from modules.yaml + the diff. Empty is fine: a PR that touched no
// known module has no module.* facts.
func parseModules(blob string) ([]*taloonerpb.TouchedModule, error) {
	if blob == "" {
		return nil, nil
	}
	var mods []*taloonerpb.TouchedModule
	if err := json.Unmarshal([]byte(blob), &mods); err != nil {
		return nil, fmt.Errorf("talooner: decode modules: %w", err)
	}
	return mods, nil
}

// primaryModule picks the module a PR is primarily about: the one with the most
// changed lines, ties broken by name (path) order so the choice is
// deterministic. Returns nil for no modules.
func primaryModule(mods []*taloonerpb.TouchedModule) *taloonerpb.TouchedModule {
	if len(mods) == 0 {
		return nil
	}
	sorted := append([]*taloonerpb.TouchedModule(nil), mods...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].GetName() < sorted[j].GetName() })
	best := sorted[0]
	for _, m := range sorted[1:] {
		if m.GetChangedLines() > best.GetChangedLines() { // strict >, so the name-order winner keeps ties
			best = m
		}
	}
	return best
}

// injectModuleFacts binds module.* in the scope to the primary touched module.
// The PR is evaluated ONCE against that module's docs; module.touched_count and
// module.documentation_urls are asserted so a ruleset can compensate (e.g.
// require narrow PRs with `attr "module.touched_count" > 1`). module.* is
// plugin-owned, so it is injected here, after re-derivation.
func injectModuleFacts(state facts.Set, mods []*taloonerpb.TouchedModule) {
	state["module.touched_count"] = float64(len(mods))
	if p := primaryModule(mods); p != nil {
		state["module.name"] = p.GetName()
		state["module.documentation_urls"] = append([]string(nil), p.GetDocumentationUrls()...)
	}
}
