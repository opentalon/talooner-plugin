package service

import (
	"strings"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

const testConfig = `{
  "tenants": [
    {
      "name": "acme",
      "api_key": "sekret",
      "models": ["claude-opus-4-8"],
      "features": ["llm_review"],
      "quota": {"calls_used": 3, "calls_limit": 100}
    }
  ]
}`

func configured(t *testing.T) *Server {
	t.Helper()
	s := New()
	if err := s.Configure(testConfig); err != nil {
		t.Fatalf("configure: %v", err)
	}
	return s
}

func whoamiCall(s *Server, args map[string]string) plugin.Response {
	return s.Execute(plugin.Request{ID: "w", Action: "whoami", Args: args})
}

func TestWhoamiValid(t *testing.T) {
	resp := whoamiCall(configured(t), map[string]string{auth.ArgAPIKey: "sekret"})
	if resp.Error != "" {
		t.Fatalf("valid whoami errored: %s", resp.Error)
	}
	var got taloonerpb.WhoamiResponse
	if err := protojson.Unmarshal([]byte(resp.StructuredContent), &got); err != nil {
		t.Fatalf("decode structured_content: %v", err)
	}
	if got.Tenant != "acme" {
		t.Errorf("tenant = %q, want acme", got.Tenant)
	}
	if got.ProtocolVersion != taloonerpb.ProtocolVersion {
		t.Errorf("protocol_version = %d, want %d", got.ProtocolVersion, taloonerpb.ProtocolVersion)
	}
	if got.GetQuota().GetLlmCallsLimit() != 100 {
		t.Errorf("quota limit = %d, want 100", got.GetQuota().GetLlmCallsLimit())
	}
	if !strings.Contains(resp.Content, "acme") {
		t.Errorf("human summary should name the tenant, got %q", resp.Content)
	}
}

// The three unhappy paths are the point: missing key, bad key, and below-floor
// version must be three distinct errors — and the auth failures must not leak
// the tenant name to an unauthenticated caller.
func TestWhoamiMissingKey(t *testing.T) {
	resp := whoamiCall(configured(t), nil)
	if resp.Error != auth.ErrMissingKey.Error() {
		t.Fatalf("error = %q, want %q", resp.Error, auth.ErrMissingKey)
	}
	if strings.Contains(resp.Error, "acme") {
		t.Error("missing-key error leaks the tenant name")
	}
	if resp.StructuredContent != "" {
		t.Error("failed auth must not return a structured payload")
	}
}

func TestWhoamiBadKey(t *testing.T) {
	resp := whoamiCall(configured(t), map[string]string{auth.ArgAPIKey: "wrong"})
	if resp.Error != auth.ErrBadKey.Error() {
		t.Fatalf("error = %q, want %q", resp.Error, auth.ErrBadKey)
	}
	if strings.Contains(resp.Error, "acme") {
		t.Error("bad-key error leaks the tenant name")
	}
}

func TestWhoamiDistinctAuthErrors(t *testing.T) {
	missing := whoamiCall(configured(t), nil).Error
	bad := whoamiCall(configured(t), map[string]string{auth.ArgAPIKey: "wrong"}).Error
	if missing == bad {
		t.Fatalf("missing-key and bad-key errors must differ; both were %q", missing)
	}
}

func TestWhoamiBelowFloor(t *testing.T) {
	// Floor defaults to ProtocolFloor; ask with a version one below it.
	if taloonerpb.ProtocolFloor == 0 {
		t.Skip("floor is zero; no below-floor value exists")
	}
	resp := whoamiCall(configured(t), map[string]string{
		auth.ArgAPIKey:     "sekret",
		ArgProtocolVersion: "0",
	})
	if resp.Error == "" {
		t.Fatal("below-floor caller should be rejected")
	}
	if !strings.Contains(resp.Error, "floor") {
		t.Errorf("below-floor error should mention the floor, got %q", resp.Error)
	}
}

func TestWhoamiAtFloorSucceeds(t *testing.T) {
	resp := whoamiCall(configured(t), map[string]string{
		auth.ArgAPIKey:     "sekret",
		ArgProtocolVersion: "1",
	})
	if resp.Error != "" {
		t.Fatalf("at-floor caller should be served, got %q", resp.Error)
	}
}

func TestWhoamiFailsClosedWithoutConfig(t *testing.T) {
	// A server that was never Configured authenticates nobody.
	resp := whoamiCall(New(), map[string]string{auth.ArgAPIKey: "sekret"})
	if resp.Error != auth.ErrBadKey.Error() {
		t.Fatalf("unconfigured server should reject all keys, got %q", resp.Error)
	}
}

func TestConfigureRejectsEmpty(t *testing.T) {
	if err := New().Configure(""); err == nil {
		t.Fatal("empty config should be rejected")
	}
	if err := New().Configure(`{"tenants":[]}`); err == nil {
		t.Fatal("config with no tenants should be rejected")
	}
}
