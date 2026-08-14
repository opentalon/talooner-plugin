package service

import (
	"sort"
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
)

// TestEveryActionIsUserOnly is the regression the skeleton exists to guard: an
// action added later without user_only would silently let an LLM reach the
// decision path. It must never pass with a non-user_only action in the set.
func TestEveryActionIsUserOnly(t *testing.T) {
	caps := New().Capabilities()
	if len(caps.Actions) == 0 {
		t.Fatal("no actions registered")
	}
	for _, a := range caps.Actions {
		if !a.UserOnly {
			t.Errorf("action %q is not user_only — LLMs could reach the decision path", a.Name)
		}
	}
}

// TestReadOnlyMatrix pins which actions are pure queries and which mutate, so a
// mutating action can't be silently flagged read_only (skipping the host's
// confirmation gate) and vice versa.
func TestReadOnlyMatrix(t *testing.T) {
	wantReadOnly := map[string]bool{
		"evaluate_pr":      false,
		"is_subscribed":    true,
		"set_subscription": false,
		"assert_facts":     false,
		"validate_ruleset": true,
		"explain_pr":       true,
		"whoami":           true,
	}
	for _, a := range New().Capabilities().Actions {
		want, known := wantReadOnly[a.Name]
		if !known {
			t.Errorf("action %q missing from the read_only matrix — classify it", a.Name)
			continue
		}
		if a.ReadOnly != want {
			t.Errorf("action %q read_only = %v, want %v", a.Name, a.ReadOnly, want)
		}
	}
}

// TestActionSet locks the declared action surface (protocol.md).
func TestActionSet(t *testing.T) {
	want := []string{
		"assert_facts", "evaluate_pr", "explain_pr", "is_subscribed",
		"set_subscription", "validate_ruleset", "whoami",
	}
	var got []string
	for _, a := range New().Capabilities().Actions {
		got = append(got, a.Name)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("action set = %v, want %v", got, want)
	}
}

func TestExecuteUnknownAction(t *testing.T) {
	resp := New().Execute(plugin.Request{ID: "1", Action: "no_such_action"})
	if resp.Error == "" {
		t.Fatal("unknown action should return an error")
	}
	if !strings.Contains(resp.Error, "unknown action") {
		t.Errorf("error = %q, want it to mention unknown action", resp.Error)
	}
	if resp.CallID != "1" {
		t.Errorf("call id = %q, want it echoed back", resp.CallID)
	}
}

// TestExecuteDispatches proves a known action reaches its registered handler.
// explain_pr is still stubbed, so a not-implemented error is the expected
// signal that dispatch worked.
func TestExecuteDispatches(t *testing.T) {
	resp := New().Execute(plugin.Request{ID: "7", Action: "explain_pr"})
	if resp.Error == "" || !strings.Contains(resp.Error, "not implemented") {
		t.Fatalf("explain_pr should dispatch to its stub handler, got %+v", resp)
	}
	if resp.CallID != "7" {
		t.Errorf("call id = %q, want it echoed back", resp.CallID)
	}
}

// TestRegisterRejectsNonUserOnly guards the enforcement in register itself, so
// the invariant fails loudly at startup and not only in the capability test.
func TestRegisterRejectsNonUserOnly(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("registering a non-user_only action should panic")
		}
	}()
	s := &Server{actions: map[string]action{}}
	s.register(plugin.ActionMsg{Name: "leaky"}, notImplemented("test"))
}
