package ruleset

import (
	"fmt"
	"strings"
)

// LintLLMReview returns the two llm_review lint warnings from llm-review.md.
// Both are warnings, never errors: a ruleset that trips them still compiles, but
// it has a spend or safety gap worth surfacing at `talooner rules validate` time.
//
//   - A rule that both reads llm_review.* in a condition and fires
//     `do llm_review`. Conditions are evaluated before actions fire, so the read
//     cannot see the result the fire produces in the same evaluation, and the
//     engine is bounded at two passes — so it never can. Almost always a mistake.
//   - A ruleset that reads llm_review.result but never handles "unclear" or
//     "error". The model being unsure or the budget being gone then falls
//     through silently — the wrong way to fail safe (llm-review.md).
//
// The scan is lexical, like CheckVerbs, but keeps string literals (fact names
// and compared values live inside them) and tracks braces (to bound each rule).
func LintLLMReview(src string) []Diagnostic {
	toks := lexLint(src)

	var diags []Diagnostic
	diags = append(diags, lintReadAndFire(toks)...)
	diags = append(diags, lintEnumCoverage(toks)...)
	return diags
}

// lintEnumCoverage warns when a ruleset reads llm_review.result but ignores the
// inconclusive verdicts.
func lintEnumCoverage(toks []lintToken) []Diagnostic {
	readsResult := false
	handled := map[string]bool{}
	for _, t := range toks {
		if t.kind != lintString {
			continue
		}
		if t.text == "llm_review.result" {
			readsResult = true
		}
		switch t.text {
		case "match", "mismatch", "unclear", "too_large", "error":
			handled[t.text] = true
		}
	}
	if !readsResult {
		return nil
	}
	var missing []string
	if !handled["unclear"] {
		missing = append(missing, `"unclear"`)
	}
	if !handled["error"] {
		missing = append(missing, `"error"`)
	}
	if len(missing) == 0 {
		return nil
	}
	return []Diagnostic{{
		Severity: SeverityWarning,
		File:     TenantFile,
		Message: fmt.Sprintf(
			"ruleset reads llm_review.result but never handles %s; an unsure model or an exhausted budget then falls through silently",
			strings.Join(missing, " or ")),
		Hint: `add rules matching llm_review.result == "unclear" and == "error" so an inconclusive review fails loudly, not silently`,
	}}
}

// lintReadAndFire warns per rule when the same rule both reads llm_review.* and
// fires do llm_review.
func lintReadAndFire(toks []lintToken) []Diagnostic {
	var diags []Diagnostic
	for i := 0; i < len(toks); i++ {
		if toks[i].kind != lintWord || toks[i].text != "rule" {
			continue
		}
		ruleLine := toks[i].line
		// Advance to the rule body's opening brace.
		j := i + 1
		for j < len(toks) && toks[j].kind != lintLBrace {
			j++
		}
		if j >= len(toks) {
			break
		}

		depth := 0
		reads, fires := false, false
		k := j
	bodyLoop:
		for ; k < len(toks); k++ {
			switch toks[k].kind {
			case lintLBrace:
				depth++
			case lintRBrace:
				depth--
				if depth == 0 {
					k++
					break bodyLoop
				}
			case lintString:
				if strings.HasPrefix(toks[k].text, "llm_review.") {
					reads = true
				}
			case lintWord:
				if toks[k].text == "do" && k+1 < len(toks) &&
					toks[k+1].kind == lintWord && toks[k+1].text == VerbLLMReview {
					fires = true
				}
			}
		}

		if reads && fires {
			diags = append(diags, Diagnostic{
				Severity: SeverityWarning,
				File:     TenantFile,
				Line:     ruleLine,
				Message:  "a rule both reads llm_review.* and fires llm_review; the read cannot see the result the fire produces, and evaluation is bounded at two passes",
				Hint:     "split it into a producer rule that fires llm_review and a separate consumer rule that reads llm_review.* (llm-review.md)",
			})
		}
		i = k - 1
	}
	return diags
}

// --- a small lexer that keeps strings and braces (CheckVerbs' scanIdents drops
// both, but fact names live in strings and rule bounds are braces) ---

type lintKind int

const (
	lintWord lintKind = iota
	lintString
	lintLBrace
	lintRBrace
)

type lintToken struct {
	kind lintKind
	text string // for lintString, the unquoted contents
	line int
}

func lexLint(src string) []lintToken {
	var toks []lintToken
	line := 1
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
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
				if src[i] == '\n' {
					line++
				}
				i++
			}
		case c == '"':
			startLine := line
			i++
			var sb strings.Builder
			for i < n && src[i] != '"' {
				if src[i] == '\\' && i+1 < n {
					if src[i+1] == '\n' {
						line++
					}
					sb.WriteByte(src[i+1])
					i += 2
					continue
				}
				if src[i] == '\n' {
					line++
				}
				sb.WriteByte(src[i])
				i++
			}
			if i < n {
				i++ // closing quote
			}
			toks = append(toks, lintToken{kind: lintString, text: sb.String(), line: startLine})
		case c == '{':
			toks = append(toks, lintToken{kind: lintLBrace, text: "{", line: line})
			i++
		case c == '}':
			toks = append(toks, lintToken{kind: lintRBrace, text: "}", line: line})
			i++
		case isIdentStart(c):
			j := i
			for j < n && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, lintToken{kind: lintWord, text: src[i:j], line: line})
			i = j
		default:
			i++
		}
	}
	return toks
}
