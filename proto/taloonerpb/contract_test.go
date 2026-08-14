package taloonerpb_test

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// TestResponseRoundTrips exercises the contract the way the wire uses it: a
// decision is a taloonerpb message serialized to JSON for structured_content.
// This is also the compile check the acceptance criterion asks for — the
// generated package is importable from outside internal/ and usable.
func TestResponseRoundTrips(t *testing.T) {
	resp := &taloonerpb.EvaluatePrResponse{
		Actions: []*taloonerpb.Action{
			{Verb: taloonerpb.Verb_VERB_BLOCK, Target: "pr.merge"},
			{Verb: taloonerpb.Verb_VERB_COMMENT, Target: "pr", Text: "no description"},
		},
		Explain: &taloonerpb.Explain{Summary: "blocked: missing description"},
	}

	data, err := protojson.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got taloonerpb.EvaluatePrResponse
	if err := protojson.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Actions) != 2 {
		t.Fatalf("actions = %d, want 2", len(got.Actions))
	}
	if got.Actions[0].Verb != taloonerpb.Verb_VERB_BLOCK {
		t.Errorf("first verb = %v, want VERB_BLOCK", got.Actions[0].Verb)
	}
}

// TestPlanModeOmitsActions pins the protocol.md guarantee: a plan-mode response
// carries plan and no actions key, so a plan can't be mistaken for something to
// execute. protojson omits empty repeated fields, so an unpopulated actions
// list simply does not appear.
func TestPlanModeOmitsActions(t *testing.T) {
	resp := &taloonerpb.EvaluatePrResponse{
		Plan: []*taloonerpb.Action{{Verb: taloonerpb.Verb_VERB_BLOCK, Target: "pr.merge"}},
	}
	data, err := protojson.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "\"actions\"") {
		t.Errorf("plan-mode response must not carry an actions key: %s", data)
	}
	if !strings.Contains(string(data), "\"plan\"") {
		t.Errorf("plan-mode response must carry a plan key: %s", data)
	}
}

func TestProtocolVersionFloor(t *testing.T) {
	if taloonerpb.ProtocolVersion < taloonerpb.ProtocolFloor {
		t.Fatalf("ProtocolVersion (%d) must be >= ProtocolFloor (%d)",
			taloonerpb.ProtocolVersion, taloonerpb.ProtocolFloor)
	}
}
