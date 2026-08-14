package service

import (
	"fmt"
	"strconv"
	"time"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/facts"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// scopeKey builds the PR scope key from repo + pr args, validating both.
func scopeKey(req plugin.Request) (string, error) {
	repo := req.Args["repo"]
	if repo == "" {
		return "", fmt.Errorf("talooner: repo is required")
	}
	n, err := strconv.Atoi(req.Args["pr"])
	if err != nil {
		return "", fmt.Errorf("talooner: invalid pr %q: must be a number", req.Args["pr"])
	}
	return facts.Key(repo, n), nil
}

// isSubscribed reports a PR's subscription state. A PR that was never seen is
// simply not subscribed — false with since 0, not an error. It backs the bot's
// cheap path: an unsubscribed PR is a skipped job, not a red check.
func (s *Server) isSubscribed(req plugin.Request) plugin.Response {
	key, err := scopeKey(req)
	if err != nil {
		return errorResponse(req, err)
	}

	s.subMu.Lock()
	sub := s.subs[key] // zero value {false, 0} for a never-seen PR
	s.subMu.Unlock()

	resp := &taloonerpb.IsSubscribedResponse{Subscribed: sub.subscribed, Since: sub.since}
	return structuredResponse(req, resp, fmt.Sprintf("%s subscribed=%v", key, sub.subscribed))
}

// setSubscription sets a PR's subscription state. Setting the same state again
// is idempotent: since marks when the current state was established and does not
// move on a no-op write.
func (s *Server) setSubscription(req plugin.Request) plugin.Response {
	key, err := scopeKey(req)
	if err != nil {
		return errorResponse(req, err)
	}
	state, err := strconv.ParseBool(req.Args["state"])
	if err != nil {
		return errorResponse(req, fmt.Errorf("talooner: invalid state %q: must be true or false", req.Args["state"]))
	}

	s.subMu.Lock()
	existing, seen := s.subs[key]
	if !seen || existing.subscribed != state {
		s.subs[key] = subscription{subscribed: state, since: time.Now().Unix()}
	}
	result := s.subs[key]
	s.subMu.Unlock()

	resp := &taloonerpb.SetSubscriptionResponse{Subscribed: result.subscribed}
	return structuredResponse(req, resp, fmt.Sprintf("%s subscribed=%v", key, result.subscribed))
}

// subscribedFor reports the stored subscription state for a scope key, so
// evaluate_pr can surface it as the pr.subscribed fact.
func (s *Server) subscribedFor(key string) bool {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	return s.subs[key].subscribed
}
