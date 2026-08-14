package service

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// Config is the plugin's config block, delivered by the host via Init before
// any Execute call (protocol.md, "Cluster auth").
type Config struct {
	// ProtocolFloor overrides the built-in floor. 0 means use
	// taloonerpb.ProtocolFloor. It can only be raised above the built-in
	// floor, never lowered below it — a config must not serve callers the
	// build can no longer serve.
	ProtocolFloor uint32         `json:"protocol_floor,omitempty"`
	Tenants       []TenantConfig `json:"tenants"`

	// FactRetentionDays is how long a PR's stored facts survive without
	// activity. 0 means the 90-day default; values below the decision-20 floor
	// (3 days) are raised to it. Decisions are never swept.
	FactRetentionDays int `json:"fact_retention_days,omitempty"`

	// RateLimitPerMinute caps requests per API key. 0 means the default (60).
	RateLimitPerMinute int `json:"rate_limit_per_minute,omitempty"`
}

// TenantConfig is one tenant's key and capabilities.
type TenantConfig struct {
	Name     string      `json:"name"`
	APIKey   string      `json:"api_key"` // supports ${ENV_VAR}
	Models   []string    `json:"models,omitempty"`
	Features []string    `json:"features,omitempty"`
	Quota    QuotaConfig `json:"quota,omitempty"`
}

// QuotaConfig is a tenant's LLM budget for the current window.
type QuotaConfig struct {
	CallsUsed  int64 `json:"calls_used,omitempty"`
	CallsLimit int64 `json:"calls_limit,omitempty"`
}

// Configure receives the plugin's config block from the host. It satisfies
// plugin.Configurable, so the host calls it via Init before any action runs. An
// empty or malformed config is a hard error: with no tenants the plugin cannot
// authenticate anyone, and starting anyway would be a silently useless plugin.
func (s *Server) Configure(configJSON string) error {
	if strings.TrimSpace(configJSON) == "" {
		return fmt.Errorf("talooner: config is required (no tenants configured)")
	}
	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("talooner: parse config: %w", err)
	}
	if len(cfg.Tenants) == 0 {
		return fmt.Errorf("talooner: at least one tenant is required")
	}

	tenants := make(map[string]auth.Tenant, len(cfg.Tenants))
	for i, tc := range cfg.Tenants {
		if tc.Name == "" {
			return fmt.Errorf("talooner: tenants[%d]: name is required", i)
		}
		key := os.Expand(tc.APIKey, os.Getenv)
		if key == "" {
			return fmt.Errorf("talooner: tenant %q: api_key is required", tc.Name)
		}
		if _, dup := tenants[key]; dup {
			return fmt.Errorf("talooner: tenant %q: api_key already used by another tenant", tc.Name)
		}
		tenants[key] = auth.Tenant{
			Name:     tc.Name,
			Models:   tc.Models,
			Features: tc.Features,
			Quota:    auth.Quota{CallsUsed: tc.Quota.CallsUsed, CallsLimit: tc.Quota.CallsLimit},
		}
	}

	reg, err := auth.NewRegistry(tenants)
	if err != nil {
		return fmt.Errorf("talooner: %w", err)
	}

	floor := taloonerpb.ProtocolFloor
	if cfg.ProtocolFloor > floor {
		floor = cfg.ProtocolFloor
	}

	s.auth = reg
	s.floor = floor
	s.factRetention = retentionFromDays(cfg.FactRetentionDays)
	s.limiter = newRateLimiter(cfg.RateLimitPerMinute)
	return nil
}
