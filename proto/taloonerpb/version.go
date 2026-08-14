package taloonerpb

// Protocol version, hand-maintained alongside the generated contract.
//
// The action version pinned in a tenant's workflow file and the plugin version
// running in the cluster are set by different people and neither is told when
// the other moves (protocol.md, "protocol_version"). whoami reports
// ProtocolVersion so a caller can detect skew; the plugin refuses callers below
// ProtocolFloor with one clear error rather than guessing (P-B1).
//
// Bump ProtocolVersion when the contract gains backward-compatible additions.
// Raise ProtocolFloor only on a breaking change the plugin can no longer serve
// — and only after the compatibility window the contract promises has elapsed.
const (
	// ProtocolVersion is the contract version this build implements and
	// reports via whoami.
	ProtocolVersion uint32 = 1

	// ProtocolFloor is the lowest caller protocol version the plugin will
	// serve. Calls below it are refused with a version-skew error.
	ProtocolFloor uint32 = 1
)
