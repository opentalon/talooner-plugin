package service

import (
	"fmt"
	"strconv"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// featureLLMReview is the whoami feature name a caller checks before loading a
// ruleset that uses llm_review, so config and handshake read the same
// (auth.Tenant.HasFeature).
const featureLLMReview = "llm_review"

// availableFeatures is the tenant's configured features minus any the running
// deployment can't honour. In standalone mode (TCP, no host) there is no
// callback channel to perform the model call, so llm_review is withdrawn — the
// caller warns at ruleset-load time instead of failing on the first PR
// (llm-review.md).
func (s *Server) availableFeatures(tenant auth.Tenant) []string {
	if !s.standalone {
		return tenant.Features
	}
	out := make([]string, 0, len(tenant.Features))
	for _, f := range tenant.Features {
		if f == featureLLMReview {
			continue
		}
		out = append(out, f)
	}
	return out
}

// ArgProtocolVersion is the optional arg by which a caller declares its
// protocol version so the plugin can reject a below-floor caller at the
// handshake instead of misbehaving mid-evaluation.
const ArgProtocolVersion = "protocol_version"

// whoami is the capability handshake the bot gates every run on — not an
// identity check. It authenticates the caller's API key, rejects a below-floor
// protocol version, and returns the tenant's identity and capabilities so the
// caller can tell (for example) whether llm_review is available before loading
// a ruleset that depends on it.
//
// It fails closed. Missing key, bad key and below-floor version are three
// distinct errors, and the two auth failures reveal nothing about any tenant —
// the version check runs only after auth, so its floor-naming message never
// reaches an unauthenticated caller.
func (s *Server) whoami(req plugin.Request) plugin.Response {
	tenant, err := s.auth.Authenticate(req.Args[auth.ArgAPIKey])
	if err != nil {
		return errorResponse(req, err)
	}

	if raw := req.Args[ArgProtocolVersion]; raw != "" {
		caller, perr := strconv.ParseUint(raw, 10, 32)
		if perr != nil {
			return errorResponse(req, fmt.Errorf("talooner: invalid protocol_version %q", raw))
		}
		if uint32(caller) < s.floor {
			return errorResponse(req, fmt.Errorf(
				"talooner: caller protocol version %d is below this plugin's floor %d; upgrade the talooner action",
				caller, s.floor))
		}
	}

	features := s.availableFeatures(tenant)
	resp := &taloonerpb.WhoamiResponse{
		Tenant:          tenant.Name,
		ProtocolVersion: taloonerpb.ProtocolVersion,
		Models:          tenant.Models,
		Features:        features,
		Quota: &taloonerpb.Quota{
			// Live counter, seeded from config and decremented as calls are made,
			// so the caller sees remaining budget rather than the startup value.
			LlmCallsUsed:  s.LLMCallsUsed(tenant),
			LlmCallsLimit: tenant.Quota.CallsLimit,
		},
	}
	summary := fmt.Sprintf("tenant=%s protocol_version=%d models=%d features=%v",
		tenant.Name, taloonerpb.ProtocolVersion, len(tenant.Models), features)
	return structuredResponse(req, resp, summary)
}
