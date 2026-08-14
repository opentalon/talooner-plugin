package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

func setSub(s *Server, repo, pr, state string) plugin.Response {
	return s.Execute(plugin.Request{ID: "set", Action: "set_subscription",
		Args: map[string]string{"repo": repo, "pr": pr, "state": state}})
}

func isSub(t *testing.T, s *Server, repo, pr string) *taloonerpb.IsSubscribedResponse {
	t.Helper()
	resp := s.Execute(plugin.Request{ID: "is", Action: "is_subscribed",
		Args: map[string]string{"repo": repo, "pr": pr}})
	if resp.Error != "" {
		t.Fatalf("is_subscribed errored: %s", resp.Error)
	}
	out := &taloonerpb.IsSubscribedResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// A PR that was never seen is not subscribed — false, since 0, and NOT an error.
func TestIsSubscribedNeverSeen(t *testing.T) {
	got := isSub(t, New(), "acme/api", "1")
	if got.Subscribed {
		t.Error("never-seen PR should not be subscribed")
	}
	if got.Since != 0 {
		t.Errorf("never-seen PR since = %d, want 0", got.Since)
	}
}

func TestSetThenIsSubscribed(t *testing.T) {
	s := New()
	setSub(s, "acme/api", "7", "true")
	got := isSub(t, s, "acme/api", "7")
	if !got.Subscribed {
		t.Error("PR should be subscribed after set")
	}
	if got.Since == 0 {
		t.Error("since should be set once subscribed")
	}
}

// Setting the same state twice is idempotent: since does not move.
func TestSetSubscriptionIdempotent(t *testing.T) {
	s := New()
	setSub(s, "acme/api", "7", "true")
	since1 := isSub(t, s, "acme/api", "7").Since

	setSub(s, "acme/api", "7", "true") // same state again
	since2 := isSub(t, s, "acme/api", "7").Since

	if since1 != since2 {
		t.Errorf("since moved on an idempotent re-set: %d -> %d", since1, since2)
	}
}

func TestSetSubscriptionResponseState(t *testing.T) {
	resp := setSub(New(), "acme/api", "7", "true")
	out := &taloonerpb.SetSubscriptionResponse{}
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Subscribed {
		t.Error("set_subscription(true) should report subscribed=true")
	}
}

func TestSetSubscriptionInvalidState(t *testing.T) {
	resp := setSub(New(), "acme/api", "7", "maybe")
	if resp.Error == "" || !strings.Contains(resp.Error, "state") {
		t.Errorf("invalid state should error naming state, got %q", resp.Error)
	}
}

// Subscription is a fact: after subscribing, a rule reading pr.subscribed fires.
func TestEvaluatePRSeesSubscription(t *testing.T) {
	const rule = `import "talooner.tln"

rule "Only when subscribed" {
  for records where type == "pr" and attr "pr.subscribed" == true
  do emit "subscribed"
}`
	s := New()
	fires := func() bool {
		resp := s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
			"repo": "acme/api", "pr": "7", "ruleset": rule, "facts": "{}",
		}})
		if resp.Error != "" {
			t.Fatalf("evaluate_pr errored: %s", resp.Error)
		}
		out := &taloonerpb.EvaluatePrResponse{}
		if err := protojson.Unmarshal([]byte(resp.StructuredContent), out); err != nil {
			t.Fatal(err)
		}
		for _, a := range out.Actions {
			if a.Verb == taloonerpb.Verb_VERB_EMIT && a.Name == "subscribed" {
				return true
			}
		}
		return false
	}

	if fires() {
		t.Error("rule should not fire before subscribing")
	}
	setSub(s, "acme/api", "7", "true")
	if !fires() {
		t.Error("rule should fire once pr.subscribed is true")
	}
}
