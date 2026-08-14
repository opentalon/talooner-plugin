package service

import (
	"fmt"
	"strconv"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/internal/auth"
	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

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

	resp := &taloonerpb.WhoamiResponse{
		Tenant:          tenant.Name,
		ProtocolVersion: taloonerpb.ProtocolVersion,
		Models:          tenant.Models,
		Features:        tenant.Features,
		Quota: &taloonerpb.Quota{
			LlmCallsUsed:  tenant.Quota.CallsUsed,
			LlmCallsLimit: tenant.Quota.CallsLimit,
		},
	}
	summary := fmt.Sprintf("tenant=%s protocol_version=%d models=%d features=%v",
		tenant.Name, taloonerpb.ProtocolVersion, len(tenant.Models), tenant.Features)
	return structuredResponse(req, resp, summary)
}
