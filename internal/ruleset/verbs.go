package ruleset

import (
	"fmt"
	"strings"
)

// AllowedVerbs is the closed abstract action vocabulary a rule may use in a
// `do <verb>` clause (engine.md, "The verb list is ours to enforce"). Adding a
// verb here means adding a matching executor in the bot repo; keeping the two
// sets identical is what stops them drifting.
//
// llm_review is the exception: it is a valid verb but the plugin performs it
// itself (via the OpenTalon host) rather than returning it to the bot, so it
// never appears in the returned action list and has no bot-side executor. It
// still belongs here because CheckVerbs must accept `do llm_review` as valid
// source (llm-review.md).
var AllowedVerbs = []string{"approve", "block", "comment", "assign", "require", "notify", "emit", "llm_review"}

// VerbLLMReview is the one verb this plugin executes instead of returning. It is
// intercepted after evaluation, run against the host, and its result asserted as
// llm_review.* facts for a second engine pass — never mapped into the returned
// actions[] (engine.md §5, llm-review.md).
const VerbLLMReview = "llm_review"

var allowedVerbSet = func() map[string]bool {
	m := make(map[string]bool, len(AllowedVerbs))
	for _, v := range AllowedVerbs {
		m[v] = true
	}
	return m
}()

// dispatchVerbs read like actions and parse fine as Tln, but describe work
// the tenant's CI does and asserts back via assert_facts — not something the
// plugin performs. They are rejected with a pointer to the facts API rather
// than accepted and silently ignored.
var dispatchVerbs = map[string]bool{
	"deploy_preview":    true,
	"screenshot":        true,
	"scan_dependencies": true,
}

// CheckVerbs scans a ruleset source for `do <verb>` clauses whose verb is
// outside AllowedVerbs and returns one error diagnostic per offender.
//
// tln-language does not validate verb names — it parses `do anything "x"` and
// hands it back — so this is the only check between a typo and a silently
// dropped action. The scan is lexical (comments and string literals are
// skipped) because the SDK only surfaces verbs for rules that fire against
// facts, whereas validation must see every declared verb.
func CheckVerbs(src string) []Diagnostic {
	var diags []Diagnostic
	for _, occ := range doVerbs(src) {
		if allowedVerbSet[occ.text] {
			continue
		}
		diags = append(diags, Diagnostic{
			Severity: SeverityError,
			File:     TenantFile,
			Line:     occ.line,
			Col:      occ.col,
			Message:  unknownVerbMessage(occ.text),
			Hint:     verbHint(occ.text),
		})
	}
	return diags
}

func unknownVerbMessage(verb string) string {
	return fmt.Sprintf("unknown action verb %q; valid verbs are: %s", verb, strings.Join(AllowedVerbs, ", "))
}

func verbHint(verb string) string {
	switch {
	case dispatchVerbs[verb]:
		return "this is not an action verb — the tenant's CI does this work and asserts the result via assert_facts, then a rule reacts to the fact"
	case verb == "reject":
		return `there is no "reject" verb — "request changes" is how "block" renders on GitHub; use: do block "pr.merge"`
	default:
		return "check for a typo against the valid verb list"
	}
}

// ident is one identifier token with its 1-based source position.
type ident struct {
	text string
	line int
	col  int
}

// doVerbs returns the verb token following each `do` keyword. It walks the
// source skipping line comments (//), block comments (/* */) and string
// literals (with escapes), so a "do foo" inside a string or comment is never
// mistaken for an action clause.
func doVerbs(src string) []ident {
	toks := scanIdents(src)
	var out []ident
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].text == "do" {
			out = append(out, toks[i+1])
		}
	}
	return out
}

func scanIdents(src string) []ident {
	var toks []ident
	line, col := 1, 1
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			line++
			col = 1
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			col += 2
			for i < n {
				if src[i] == '*' && i+1 < n && src[i+1] == '/' {
					i += 2 // consume the closing */
					col += 2
					break
				}
				if src[i] == '\n' {
					line++
					col = 1
				} else {
					col++
				}
				i++
			}
		case c == '"':
			i++
			col++
			for i < n && src[i] != '"' {
				switch {
				case src[i] == '\\' && i+1 < n:
					if src[i+1] == '\n' {
						line++
						col = 1
					} else {
						col += 2
					}
					i += 2
				case src[i] == '\n':
					line++
					col = 1
					i++
				default:
					col++
					i++
				}
			}
			if i < n {
				i++ // closing quote
				col++
			}
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, ident{text: src[i:j], line: line, col: col})
			col += j - i
			i = j
		default:
			col++
			i++
		}
	}
	return toks
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
