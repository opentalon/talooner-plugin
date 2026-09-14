package ruleset

// CheckLLMResultCoverage scans a tenant ruleset for a rule that reacts to
// attr "unit.llm_result" == "mismatch" without any rule also reacting to
// "unclear" or "error". Both verdicts mean the model never gave a real
// answer (uncertain, or the quota ran out) — a ruleset that only matches
// match/mismatch does nothing on either, which fails safe (no approval) but
// silently, the wrong way to fail silently (llm-review.md, "Why the ruleset
// must handle unclear and error").
func CheckLLMResultCoverage(tenantSource string) []Diagnostic {
	values := llmResultValues(tenantSource)
	if !values["mismatch"] || values["unclear"] || values["error"] {
		return nil
	}
	return []Diagnostic{{
		Severity: SeverityWarning,
		File:     TenantFile,
		Message: `ruleset reacts to attr "unit.llm_result" == "mismatch" but has no rule ` +
			`for "unclear" or "error" — a model that is unsure or out of quota will ` +
			`silently pass the PR through instead of failing loudly`,
	}}
}

// llmResultValues returns the set of string literals compared against
// attr "unit.llm_result" anywhere in src. It reuses tokenize's comment/string
// handling, so a comparison inside a comment or nested string is never
// mistaken for one in a `for`/`and` clause.
func llmResultValues(src string) map[string]bool {
	toks := tokenize(src)
	values := map[string]bool{}
	for i := 0; i+2 < len(toks); i++ {
		if toks[i].kind == kIdent && toks[i].text == "attr" &&
			toks[i+1].kind == kString && toks[i+1].text == "unit.llm_result" &&
			toks[i+2].kind == kString {
			values[toks[i+2].text] = true
		}
	}
	return values
}
