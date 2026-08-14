package service

import (
	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// Decision is the audit record of one evaluate_pr, persisted before the
// response is returned so a decision is queryable even if the caller — a
// workflow run that can be cancelled mid-flight — never receives it.
//
// It records the facts at evaluation time, the ruleset content hash, which
// rules fired and which did not, the returned actions and the explanation.
// Recording which rules did NOT fire is what later lets explain_pr answer "why
// wasn't this approved". Note: the per-condition reason a rule did not fire, and
// the set suppressed by defeasible resolution, need engine introspection not yet
// exposed by the tln SDK / defeasible resolution (P-C1); those enrich this
// record later.
type Decision struct {
	Repo        string
	PR          int
	HeadSHA     string
	RulesetHash string
	Facts       facts.Set
	Fired       []string
	NotFired    []string
	Actions     []*taloonerpb.Action
	Explain     *taloonerpb.Explain
	At          int64 // unix seconds
}

func decisionKey(repo string, pr int, headSHA string) string {
	return facts.Key(repo, pr) + "@" + headSHA
}

// persistDecision stores a decision. Decisions are retained indefinitely
// (facts.md: facts expire, decisions outlive them); a configurable sweeper is
// P-C7. Keyed by (repo, pr, head_sha) so each reviewed sha keeps its own record.
func (s *Server) persistDecision(d Decision) {
	s.decMu.Lock()
	s.decisions[decisionKey(d.Repo, d.PR, d.HeadSHA)] = d
	s.decMu.Unlock()
}

// decision returns a persisted decision, if any. It backs explain_pr (P-C5).
func (s *Server) decision(repo string, pr int, headSHA string) (Decision, bool) {
	s.decMu.Lock()
	defer s.decMu.Unlock()
	d, ok := s.decisions[decisionKey(repo, pr, headSHA)]
	return d, ok
}
