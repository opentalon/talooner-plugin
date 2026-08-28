package facts

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Set is a decoded fact blob: attribute name → value. Values are bool, float64,
// string, or []string — the four types that survive the JSON round trip from
// the bot (talooner internal/facts.Set).
type Set map[string]any

// botPrefixes are the namespaces the bot re-derives on every evaluate_pr. Facts
// in these namespaces that are absent from a request are retracted; everything
// else survives — llm_review.* is pinned to head sha and is the plugin's, not
// the bot's, to retract; module.*/team.* are tenant lookup tables; custom
// tenant-CI facts (preview.*, …) are pushed out of band (facts.md,
// "Namespaces").
var botPrefixes = []string{"pr.", "user.", "repo.", "review."}

// BotOwns reports whether an attribute is in a namespace the bot re-derives on
// every run.
func BotOwns(attr string) bool {
	for _, p := range botPrefixes {
		if strings.HasPrefix(attr, p) {
			return true
		}
	}
	return false
}

// reservedPrefixes are the namespaces no tenant may write via assert_facts.
// Wider than botPrefixes: it also covers event.* (asserted by `do emit`) and
// llm_review.* (this plugin, pinned to head sha). Without this check a tenant's
// CI could POST pr.tests_passing=true and defeat the entire ruleset — and since
// CI POSTs directly to the cluster, this is the only check that exists
// (facts.md, "Namespace enforcement lives here").
var reservedPrefixes = []string{"pr.", "user.", "repo.", "review.", "event.", "llm_review.", "unit."}

// Reserved reports whether an attribute is in a reserved namespace and, if so,
// names it (e.g. "pr.*"). These may not be asserted by a tenant.
func Reserved(attr string) (namespace string, reserved bool) {
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(attr, p) {
			return strings.TrimSuffix(p, ".") + ".*", true
		}
	}
	return "", false
}

// Rederive computes a scope's fact set after an evaluate_pr: the prior set with
// every bot-owned fact dropped, overlaid with the request's facts.
//
// This is full re-derivation, never deltas. A bot fact present before but
// absent now is gone — the approving fact set is replaced, the rule stops
// firing, the bot dismisses the review. Non-bot facts (custom tenant-CI facts,
// llm_review.*, module.*/team.*) survive untouched: they are not part of the
// bot's re-derivation.
func Rederive(prior, request Set) Set {
	next := make(Set, len(prior)+len(request))
	for k, v := range prior {
		if !BotOwns(k) {
			next[k] = v
		}
	}
	for k, v := range request {
		next[k] = v
	}
	return next
}

// Decode parses the evaluate_pr `facts` JSON blob into a Set. JSON arrays are
// normalised to []string; unsupported shapes (nested objects, null, mixed
// arrays) are rejected rather than silently dropped, because an unset fact and a
// malformed one are not the same thing.
func Decode(blob string) (Set, error) {
	if strings.TrimSpace(blob) == "" {
		return Set{}, nil
	}
	var raw map[string]any
	dec := json.NewDecoder(strings.NewReader(blob))
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("facts: decode blob: %w", err)
	}
	out := make(Set, len(raw))
	for k, v := range raw {
		nv, err := normalize(v)
		if err != nil {
			return nil, fmt.Errorf("facts: attribute %q: %w", k, err)
		}
		out[k] = nv
	}
	return out, nil
}

func normalize(v any) (any, error) {
	switch x := v.(type) {
	case bool, string, float64:
		return x, nil
	case []any:
		ss := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("list contains a non-string element (%T)", e)
			}
			ss = append(ss, s)
		}
		return ss, nil
	case nil:
		return nil, fmt.Errorf("null is not a fact value; omit the attribute instead")
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}
