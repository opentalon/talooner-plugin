package service

import (
	"fmt"
	"sort"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// assertFacts is the custom-facts path: tenant CI POSTs facts that rules react
// to at the next evaluation. It enforces namespaces — writes to reserved
// namespaces (pr./user./repo./review./event./llm_review.) are rejected — then
// stores the accepted facts. Store-only in v1 (decision 20): it returns what was
// accepted and rejected and carries NO action list.
//
// This is the only namespace check that exists: CI POSTs directly to the
// cluster, there is no bot endpoint filtering first. Without it a tenant could
// assert pr.tests_passing=true and defeat the ruleset.
func (s *Server) assertFacts(req plugin.Request) plugin.Response {
	key, err := scopeKey(req)
	if err != nil {
		return errorResponse(req, err)
	}
	set, err := facts.Decode(req.Args["facts"])
	if err != nil {
		return errorResponse(req, err)
	}

	accepted := make([]string, 0, len(set))
	var rejected []*taloonerpb.RejectedFact
	toStore := facts.Set{}
	for attr, v := range set {
		if ns, reserved := facts.Reserved(attr); reserved {
			rejected = append(rejected, &taloonerpb.RejectedFact{
				Attribute: attr,
				Reason:    fmt.Sprintf("namespace %s is reserved and cannot be asserted by a tenant", ns),
			})
			continue
		}
		accepted = append(accepted, attr)
		toStore[attr] = v
	}

	// Persist only the accepted facts; a rejected fact is never written.
	if len(toStore) > 0 {
		s.factMu.Lock()
		cur := s.tenantFacts[key]
		if cur == nil {
			cur = facts.Set{}
		}
		for k, v := range toStore {
			cur[k] = v
		}
		s.tenantFacts[key] = cur
		s.factMu.Unlock()
	}

	sort.Strings(accepted)
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].Attribute < rejected[j].Attribute })

	resp := &taloonerpb.AssertFactsResponse{Accepted: accepted, Rejected: rejected}
	summary := fmt.Sprintf("%s: accepted %d, rejected %d", key, len(accepted), len(rejected))
	return structuredResponse(req, resp, summary)
}

// tenantFactsFor returns a copy of the custom facts stored for a scope, for
// merging into an evaluation. A copy so a concurrent assert can't mutate a set
// an evaluation is reading.
func (s *Server) tenantFactsFor(key string) facts.Set {
	s.factMu.Lock()
	defer s.factMu.Unlock()
	src := s.tenantFacts[key]
	if len(src) == 0 {
		return nil
	}
	out := make(facts.Set, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
