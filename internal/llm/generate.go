package llm

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// generatePromptTemplate is the ruleset-scaffolding prompt for
// generate_ruleset. It lives in a .txt file, never a Go string literal, for
// the same reason promptTemplate does (review.go).
//
//go:embed prompts/generate_ruleset.txt
var generatePromptTemplate string

// generateRetryPromptTemplate is generatePromptTemplate's counterpart for a
// fix-up attempt: same syntax reference and output contract, plus the prior
// attempt's source and the compiler's complaint about it. A retry is still a
// single one-shot subprocess call with no memory of the first one, so it has
// to be self-contained rather than a delta on top of the first prompt.
//
//go:embed prompts/generate_ruleset_retry.txt
var generateRetryPromptTemplate string

// GenerateInput is what the model sees to scaffold a ruleset: a caller-built
// text summary of the target repo. The plugin never touches the repo itself.
// Prior is nil on the first attempt; a caller retrying after a compile/test
// failure sets it so the model can see and fix its own mistake.
type GenerateInput struct {
	RepoSummary string
	Prior       *PriorAttempt
}

// PriorAttempt is a previous generation that failed verification, threaded
// into a retry prompt so the model fixes the specific problem instead of
// guessing again from scratch.
type PriorAttempt struct {
	Ruleset     string
	RulesetTest string
	Error       string
}

// generatePayload is the JSON shape the prompt asks the sub-agent to return.
type generatePayload struct {
	Ruleset     string `json:"ruleset"`
	RulesetTest string `json:"ruleset_test"`
}

// Generate asks the host to scaffold a rules.tln + rules.tln.test pair for a
// repo. It never returns an error: a failed or unparseable call reports
// ok=false with an explanation, so the caller can fall back to a known-good
// starter rather than propagating a compiler-facing error for what is really
// "the model didn't produce usable output" (same convention as Review).
func Generate(ctx context.Context, host HostCaller, in GenerateInput) (ruleset, testSource string, ok bool, explanation string) {
	if host == nil {
		return "", "", false, "generate_ruleset is unavailable: this plugin is running without an OpenTalon host to perform the call"
	}

	res, err := host.RunAction(ctx, subprocessPlugin, subprocessAction, map[string]string{
		"task": renderGeneratePrompt(in),
		// Scaffolding a ruleset is a pure generation task: no tools needed, and
		// none wanted — the repo summary is caller-controlled text, same
		// prompt-injection posture as llm_review's diff/doc input.
		"tools":          "none",
		"max_iterations": "1",
	})
	if err != nil {
		return "", "", false, fmt.Sprintf("the model call failed: %v", err)
	}

	payload, decodeErr := decodeGeneratePayload(res.StructuredContent)
	if decodeErr != nil {
		payload, decodeErr = decodeGeneratePayload(res.Content)
	}
	if decodeErr != nil {
		return "", "", false, fmt.Sprintf("the model did not return a parseable ruleset: %v", decodeErr)
	}
	if strings.TrimSpace(payload.Ruleset) == "" || strings.TrimSpace(payload.RulesetTest) == "" {
		return "", "", false, "the model returned an empty ruleset or test source"
	}
	return ensureTrailingNewline(payload.Ruleset), ensureTrailingNewline(payload.RulesetTest), true, ""
}

// ensureTrailingNewline gives model output a POSIX-clean single trailing
// newline — models are inconsistent about ending JSON-embedded source with
// one, and a missing one is flagged by every linter/diff tool downstream.
func ensureTrailingNewline(s string) string {
	return strings.TrimRight(s, "\n") + "\n"
}

func renderGeneratePrompt(in GenerateInput) string {
	if in.Prior == nil {
		r := strings.NewReplacer("{{REPO_SUMMARY}}", in.RepoSummary)
		return r.Replace(generatePromptTemplate)
	}
	r := strings.NewReplacer(
		"{{REPO_SUMMARY}}", in.RepoSummary,
		"{{PRIOR_RULESET}}", in.Prior.Ruleset,
		"{{PRIOR_RULESET_TEST}}", in.Prior.RulesetTest,
		"{{COMPILE_ERROR}}", in.Prior.Error,
	)
	return r.Replace(generateRetryPromptTemplate)
}

// decodeGeneratePayload tolerates a model that wraps its JSON in prose or a
// code fence by scanning for the first balanced JSON object; a clean object
// parses directly. Braces inside the ruleset/ruleset_test string VALUES do
// not confuse this: they are inside a JSON string, and json.Unmarshal
// resolves the object's own outer braces regardless of what a nested string
// contains.
func decodeGeneratePayload(raw string) (generatePayload, error) {
	raw = strings.TrimSpace(raw)
	var p generatePayload
	if err := json.Unmarshal([]byte(raw), &p); err == nil {
		return p, nil
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(raw[start:end+1]), &p); err == nil {
			return p, nil
		}
	}
	return generatePayload{}, fmt.Errorf("no valid JSON object found")
}
