package service

import "time"

const (
	// defaultFactRetention is how long a PR's stored facts survive without
	// activity before the sweeper removes them.
	defaultFactRetention = 90 * 24 * time.Hour

	// minFactRetention is the floor decision 20 puts under the number: an
	// externally asserted fact sits unread until someone runs /review, so a
	// retention shorter than "nobody touched this PR for a few days" would drop
	// facts that were never used. Config cannot lower retention below this.
	minFactRetention = 72 * time.Hour
)

// touchScope records activity on a PR scope, resetting its retention clock. Any
// assert_facts or (execute-mode) evaluate_pr on the scope keeps its facts alive.
func (s *Server) touchScope(key string) {
	s.factMu.Lock()
	s.lastActivity[key] = time.Now().Unix()
	s.factMu.Unlock()
}

// Sweep removes the stored facts of every scope idle longer than the retention
// window as of now, and reports how many it swept. Decisions and explanations
// are never swept — they outlive facts (facts.md), so "why did the bot block
// this?" still answers months later.
//
// It is a scan plus per-doc delete, because tln-db has no bulk delete. It is
// idempotent and resumable: deleting an already-swept scope is a no-op, and an
// interrupted run simply leaves the remainder for the next scan — so a mid-run
// interruption never double-deletes or skips.
func (s *Server) Sweep(now time.Time) int {
	cutoff := now.Add(-s.factRetention).Unix()
	s.factMu.Lock()
	defer s.factMu.Unlock()
	var swept int
	for key, last := range s.lastActivity {
		if last < cutoff {
			delete(s.tenantFacts, key)
			delete(s.lastActivity, key)
			swept++
		}
	}
	return swept
}

// retentionFromDays converts a configured day count to a duration, applying the
// default and the decision-20 floor.
func retentionFromDays(days int) time.Duration {
	d := defaultFactRetention
	if days > 0 {
		d = time.Duration(days) * 24 * time.Hour
	}
	if d < minFactRetention {
		d = minFactRetention
	}
	return d
}
