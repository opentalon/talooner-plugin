// Package llm performs Talooner's one LLM call — llm_review — through the
// OpenTalon host rather than an embedded provider SDK.
//
// The cluster holds the tenant's provider credentials, but they live in the
// host, not this plugin (decision 3, llm-review.md). So instead of calling a
// model directly, the plugin fires the host builtin `_subprocess.run` over the
// plugin callback channel: the host runs a bounded single-turn sub-agent with
// cluster credentials, meters the token spend (opentalon_llm_* / opentalon_plugin_*
// metrics), and returns the answer inline. This package builds the prompt,
// makes that callback, and constrains the answer to a fixed verdict enum.
package llm

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opentalon/opentalon/pkg/plugin"
)

// promptTemplate is the review prompt. It lives in a .txt file, never a Go
// string literal — opentalon/CLAUDE.md is explicit about this and CI enforces
// it (llm-review.md, "Constraints").
//
//go:embed prompts/llm_review.txt
var promptTemplate string

// PromptVersion identifies the exact prompt bytes that produced a verdict. It is
// part of the fact-cache key, so editing the prompt invalidates cached results
// (a new prompt is a new question) without any manual bump. Twelve hex chars of
// the template's sha256 is plenty to distinguish revisions.
var PromptVersion = promptHash()

func promptHash() string {
	sum := sha256.Sum256([]byte(promptTemplate))
	return hex.EncodeToString(sum[:])[:12]
}

// Host builtin the review call targets. `_subprocess.run` forks a focused
// sub-agent that runs its own agent loop and returns its answer synchronously
// (opentalon internal/orchestrator/orchestrator.go, subCap).
const (
	subprocessPlugin = "_subprocess"
	subprocessAction = "run"
)

// Verdict is the fixed output enum. The enum drives decisions; the free-text
// explanation is only ever rendered as escaped, quoted text (llm-review.md).
type Verdict string

const (
	VerdictMatch    Verdict = "match"
	VerdictMismatch Verdict = "mismatch"
	VerdictUnclear  Verdict = "unclear"
	VerdictTooLarge Verdict = "too_large"
	VerdictError    Verdict = "error"
)

// valid reports whether v is one of the model-producible verdicts. too_large and
// error are produced by the plugin, not the model, so a model claiming them is
// treated as unclear.
func (v Verdict) modelProducible() bool {
	return v == VerdictMatch || v == VerdictMismatch || v == VerdictUnclear
}

// Result is one review outcome.
type Result struct {
	Verdict     Verdict
	Explanation string
}

// ReviewInput is everything the model sees for one code unit's review. DocContent
// is the governing documentation text, read from the base branch by the caller so
// a fork PR cannot rewrite the thing it is judged against. Diff is attacker-
// controlled on a fork PR; the prompt treats it as data (llm-review.md, "Prompt
// injection is assumed").
type ReviewInput struct {
	Unit          string
	DocContent    string
	Diff          string
	DiffTruncated bool
}

// HostCaller is the subset of plugin.HostCaller this package needs. Declaring it
// here (rather than importing the concrete callback stream) is what lets tests
// pass a fake and keeps the package hermetic — the real provider call happens on
// the host.
type HostCaller interface {
	RunAction(ctx context.Context, plugin, action string, args map[string]string) (plugin.CallResult, error)
}

// Review asks the host to judge one change against its documentation and returns
// a constrained verdict. It never returns an error: every failure mode maps to a
// verdict (error / too_large / unclear) so a failed review lands as a fact and a
// ruleset can react, rather than crashing the run or — worse — silently
// approving (llm-review.md, "Why the ruleset must handle unclear and error").
func Review(ctx context.Context, host HostCaller, in ReviewInput) Result {
	if host == nil {
		return Result{
			Verdict:     VerdictError,
			Explanation: "llm_review is unavailable: this plugin is running without an OpenTalon host to perform the call",
		}
	}
	// A truncated diff can't be reviewed honestly — the contradiction might be in
	// the part that was cut. Fail as too_large rather than judging a partial diff.
	if in.DiffTruncated {
		return Result{
			Verdict:     VerdictTooLarge,
			Explanation: "the diff was truncated before it reached the reviewer; the change is too large to review",
		}
	}

	res, err := host.RunAction(ctx, subprocessPlugin, subprocessAction, map[string]string{
		"task": renderPrompt(in),
		// A pure judgement: no tools, so a prompt-injected diff can't induce a
		// side effect and the single turn ends in an answer, not a tool call.
		// "none" is an explicit no-tools mode on a current host (opentalon#341);
		// on an older host it is an allowlist entry that matches no tool, which
		// is the same zero-tools result. Note "" would mean ALL tools.
		"tools":          "none",
		"max_iterations": "1",
	})
	if err != nil {
		return Result{
			Verdict:     VerdictError,
			Explanation: fmt.Sprintf("the model call failed: %v", err),
		}
	}

	return parseVerdict(res)
}

// renderPrompt substitutes the input into the template.
func renderPrompt(in ReviewInput) string {
	r := strings.NewReplacer(
		"{{UNIT}}", in.Unit,
		"{{DOC_CONTENT}}", in.DocContent,
		"{{DIFF}}", in.Diff,
	)
	return r.Replace(promptTemplate)
}

// verdictPayload is the JSON shape the prompt asks the sub-agent to return.
type verdictPayload struct {
	Verdict     string `json:"verdict"`
	Explanation string `json:"explanation"`
}

// parseVerdict extracts the constrained verdict from the host's reply. It prefers
// the structured channel, falls back to the human-readable content, and treats
// anything it cannot parse into a model-producible verdict as unclear — an
// unparseable answer is exactly the case the ruleset must handle as "not sure",
// never as approval.
func parseVerdict(res plugin.CallResult) Result {
	raw := res.StructuredContent
	if strings.TrimSpace(raw) == "" {
		raw = res.Content
	}
	payload, ok := decodePayload(raw)
	if !ok {
		return Result{
			Verdict:     VerdictUnclear,
			Explanation: "the model did not return a parseable verdict",
		}
	}
	v := Verdict(strings.TrimSpace(strings.ToLower(payload.Verdict)))
	if !v.modelProducible() {
		return Result{
			Verdict:     VerdictUnclear,
			Explanation: "the model returned an unrecognised verdict",
		}
	}
	return Result{Verdict: v, Explanation: strings.TrimSpace(payload.Explanation)}
}

// decodePayload tolerates a model that wraps its JSON in prose or a code fence by
// scanning for the first balanced JSON object; a clean object parses directly.
func decodePayload(raw string) (verdictPayload, bool) {
	raw = strings.TrimSpace(raw)
	var p verdictPayload
	if err := json.Unmarshal([]byte(raw), &p); err == nil && p.Verdict != "" {
		return p, true
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(raw[start:end+1]), &p); err == nil && p.Verdict != "" {
			return p, true
		}
	}
	return verdictPayload{}, false
}
