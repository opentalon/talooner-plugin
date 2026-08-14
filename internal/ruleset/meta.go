package ruleset

import "strings"

// Priority ranks, high to low. MEDIUM is the default when a rule omits a
// priority clause (engine.md).
const (
	PriorityLow      = 1
	PriorityMedium   = 2
	PriorityHigh     = 3
	PriorityCritical = 4
)

// Meta is a rule's defeasible attributes: whether it is `strict` and its
// priority rank. It is what the plugin's conflict resolution needs that the
// engine does not surface on a fired action.
type Meta struct {
	Strict   bool
	Priority int
}

// RuleMeta returns the defeasible metadata for every rule in play — the strict
// base's and the tenant's. The engine resolves `overrides` itself but does not
// auto-defeat on `strict`/`priority` without an overrides edge, and it does not
// report a fired action's strict/priority; the plugin layers that resolution on
// top (P-C1), and this is the metadata it reads.
func RuleMeta(tenantSource string) map[string]Meta {
	out := map[string]Meta{}
	for _, src := range []string{strictBase, tenantSource} {
		parseRuleMeta(src, out)
	}
	return out
}

func parseRuleMeta(src string, out map[string]Meta) {
	toks := tokenize(src)
	for i := 0; i < len(toks); i++ {
		if toks[i].kind != kIdent || toks[i].text != "rule" {
			continue
		}
		strict := i > 0 && toks[i-1].kind == kIdent && toks[i-1].text == "strict"
		if i+1 >= len(toks) || toks[i+1].kind != kString {
			continue
		}
		name := toks[i+1].text
		out[name] = Meta{Strict: strict, Priority: rulePriority(toks, i+2)}
	}
}

// rulePriority scans a rule's `{ ... }` body starting at or after index start for
// a `priority <LEVEL>` clause, returning the rank (default MEDIUM). It respects
// brace nesting so a nested block's braces don't end the scan early.
func rulePriority(toks []token, start int) int {
	i := start
	for i < len(toks) && toks[i].kind != kLBrace {
		i++
	}
	if i >= len(toks) {
		return PriorityMedium
	}
	depth := 0
	prio := PriorityMedium
	for ; i < len(toks); i++ {
		switch toks[i].kind {
		case kLBrace:
			depth++
		case kRBrace:
			depth--
			if depth == 0 {
				return prio
			}
		case kIdent:
			if toks[i].text == "priority" && i+1 < len(toks) && toks[i+1].kind == kIdent {
				prio = priorityRank(toks[i+1].text)
			}
		}
	}
	return prio
}

func priorityRank(level string) int {
	switch strings.ToUpper(level) {
	case "CRITICAL":
		return PriorityCritical
	case "HIGH":
		return PriorityHigh
	case "LOW":
		return PriorityLow
	default:
		return PriorityMedium
	}
}

type tokKind int

const (
	kIdent tokKind = iota
	kString
	kLBrace
	kRBrace
)

type token struct {
	kind tokKind
	text string
}

// tokenize emits identifier, string and brace tokens, skipping line comments,
// block comments and string bodies — the shapes that would otherwise confuse
// rule/priority detection.
func tokenize(src string) []token {
	var toks []token
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i < n {
				if src[i] == '*' && i+1 < n && src[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
		case c == '"':
			i++
			var b strings.Builder
			for i < n && src[i] != '"' {
				if src[i] == '\\' && i+1 < n {
					b.WriteByte(src[i+1])
					i += 2
					continue
				}
				b.WriteByte(src[i])
				i++
			}
			if i < n {
				i++
			}
			toks = append(toks, token{kString, b.String()})
		case c == '{':
			toks = append(toks, token{kLBrace, "{"})
			i++
		case c == '}':
			toks = append(toks, token{kRBrace, "}"})
			i++
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, token{kIdent, src[i:j]})
			i = j
		default:
			i++
		}
	}
	return toks
}
