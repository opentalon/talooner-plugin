package ruleset

import "strings"

// Hash returns the content hash of a tenant ruleset compiled with the strict
// base — the same value Load records — without running the full compile. It is
// recorded with every persisted decision so the exact ruleset that produced a
// decision is identifiable.
func Hash(tenantSource string) string {
	return hashRuleset(strictBase, tenantSource)
}

// RuleNames returns every rule name in play for an evaluation: the strict base's
// plus the tenant's, de-duplicated in declaration order (base first). It lets a
// decision record which rules did not fire, not only which did.
func RuleNames(tenantSource string) []string {
	seen := map[string]bool{}
	var names []string
	for _, src := range []string{strictBase, tenantSource} {
		for _, n := range scanRuleNames(src) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	return names
}

// scanRuleNames finds the name string following each `rule` keyword (including
// `strict rule`). It skips line comments, block comments and string bodies so a
// "rule" inside a string is never mistaken for a declaration, mirroring the verb
// scanner.
func scanRuleNames(src string) []string {
	var names []string
	var lastIdent string
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
				i++ // closing quote
			}
			if lastIdent == "rule" {
				names = append(names, b.String())
			}
			lastIdent = ""
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(src[j]) {
				j++
			}
			lastIdent = src[i:j]
			i = j
		default:
			// Keep lastIdent across whitespace so `rule "x"` is recognised, but
			// drop it on any other punctuation so only an immediate name binds.
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				lastIdent = ""
			}
			i++
		}
	}
	return names
}
