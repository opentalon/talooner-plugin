package service

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
)

func configuredServer(t *testing.T, extra string) *Server {
	t.Helper()
	s := New()
	cfg := `{"tenants":[{"name":"acme","api_key":"k1"},{"name":"beta","api_key":"k2"}]` + extra + `}`
	if err := s.Configure(cfg); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return s
}

const gateRuleset = `import "talooner.tln"

rule "Comment" {
  for records where type == "pr" and attr "pr.open" == true
  do comment "pr" "hi"
}`

func evalGate(s *Server, key string) plugin.Response {
	args := map[string]string{
		"repo": "acme/api", "pr": "1", "head_sha": "sha", "ruleset": gateRuleset, "facts": `{"pr.open": true}`,
	}
	if key != "" {
		args[auth.ArgAPIKey] = key
	}
	return s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: args})
}

// Once configured, every action authenticates — not just whoami. A missing or
// bad key fails closed on the internet-facing endpoint.
func TestAuthEnforcedOnAllActionsWhenConfigured(t *testing.T) {
	s := configuredServer(t, "")

	if resp := evalGate(s, ""); resp.Error != auth.ErrMissingKey.Error() {
		t.Errorf("no key should be rejected on evaluate_pr, got %q", resp.Error)
	}
	if resp := evalGate(s, "wrong"); resp.Error != auth.ErrBadKey.Error() {
		t.Errorf("bad key should be rejected, got %q", resp.Error)
	}
	if resp := evalGate(s, "k1"); resp.Error != "" {
		t.Errorf("valid key should be served, got %q", resp.Error)
	}
}

// An unconfigured server (tests/dev) does not gate non-whoami actions, so the
// suite's other tests keep working; production is always configured.
func TestUnconfiguredServerSkipsGate(t *testing.T) {
	if resp := evalGate(New(), ""); resp.Error != "" {
		t.Errorf("unconfigured server should not gate, got %q", resp.Error)
	}
}

// The rate limit is per key: exhausting one key's budget does not affect another.
func TestRateLimitPerKey(t *testing.T) {
	s := configuredServer(t, `, "rate_limit_per_minute": 2`)

	if evalGate(s, "k1").Error != "" {
		t.Fatal("first k1 request should be allowed")
	}
	if evalGate(s, "k1").Error != "" {
		t.Fatal("second k1 request should be allowed")
	}
	third := evalGate(s, "k1")
	if third.Error == "" || !strings.Contains(third.Error, "rate limit") {
		t.Errorf("third k1 request should be rate limited, got %q", third.Error)
	}
	// A different key has its own bucket.
	if resp := evalGate(s, "k2"); resp.Error != "" {
		t.Errorf("k2 should be unaffected by k1's rate limit, got %q", resp.Error)
	}
}

// captureHandler collects emitted log records for assertion.
type captureHandler struct{ records []map[string]any }

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	m := map[string]any{"msg": r.Message}
	r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.String(); return true })
	h.records = append(h.records, m)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// The caller is logged — repo, pr, workflow run id, tenant — so quota spend is
// attributable to a repo. The API key is never logged.
func TestCallerLogged(t *testing.T) {
	s := configuredServer(t, "")
	cap := &captureHandler{}
	s.logger = slog.New(cap)

	s.Execute(plugin.Request{ID: "e", Action: "evaluate_pr", Args: map[string]string{
		auth.ArgAPIKey: "k1", "repo": "acme/api", "pr": "42", "head_sha": "sha",
		"workflow_run_id": "run-999", "ruleset": gateRuleset, "facts": `{"pr.open": true}`,
	}})

	if len(cap.records) == 0 {
		t.Fatal("a configured action call should log the caller")
	}
	rec := cap.records[0]
	if rec["repo"] != "acme/api" || rec["pr"] != "42" || rec["workflow_run_id"] != "run-999" {
		t.Errorf("log missing caller fields: %+v", rec)
	}
	if rec["tenant"] != "acme" {
		t.Errorf("log should name the tenant, got %+v", rec)
	}
	for _, v := range rec {
		if s, ok := v.(string); ok && strings.Contains(s, "k1") {
			t.Errorf("the API key must never be logged: %+v", rec)
		}
	}
}
