package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/opentalon/opentalon/pkg/plugin"
)

type stubHost struct {
	res plugin.CallResult
	err error
}

func (s stubHost) RunAction(_ context.Context, _, _ string, _ map[string]string) (plugin.CallResult, error) {
	return s.res, s.err
}

func TestReviewNilHostIsError(t *testing.T) {
	got := Review(context.Background(), nil, ReviewInput{DocContent: "d", Diff: "x"})
	if got.Verdict != VerdictError {
		t.Errorf("no host should be error, got %q", got.Verdict)
	}
}

func TestReviewTruncatedDiffIsTooLarge(t *testing.T) {
	host := stubHost{res: plugin.CallResult{StructuredContent: `{"verdict":"match"}`}}
	got := Review(context.Background(), host, ReviewInput{DocContent: "d", Diff: "x", DiffTruncated: true})
	if got.Verdict != VerdictTooLarge {
		t.Errorf("truncated diff should be too_large without a call, got %q", got.Verdict)
	}
}

func TestReviewParsesStructuredVerdict(t *testing.T) {
	host := stubHost{res: plugin.CallResult{StructuredContent: `{"verdict":"mismatch","explanation":"contradicts docs"}`}}
	got := Review(context.Background(), host, ReviewInput{DocContent: "d", Diff: "x"})
	if got.Verdict != VerdictMismatch {
		t.Errorf("verdict = %q, want mismatch", got.Verdict)
	}
	if got.Explanation != "contradicts docs" {
		t.Errorf("explanation = %q", got.Explanation)
	}
}

func TestReviewFallsBackToContent(t *testing.T) {
	host := stubHost{res: plugin.CallResult{Content: "sure: {\"verdict\":\"match\",\"explanation\":\"ok\"}"}}
	got := Review(context.Background(), host, ReviewInput{DocContent: "d", Diff: "x"})
	if got.Verdict != VerdictMatch {
		t.Errorf("verdict = %q, want match (parsed from content)", got.Verdict)
	}
}

func TestReviewUnparseableIsUnclear(t *testing.T) {
	host := stubHost{res: plugin.CallResult{Content: "I could not decide"}}
	got := Review(context.Background(), host, ReviewInput{DocContent: "d", Diff: "x"})
	if got.Verdict != VerdictUnclear {
		t.Errorf("unparseable answer should be unclear, got %q", got.Verdict)
	}
}

func TestReviewRejectsPluginProducedVerdictFromModel(t *testing.T) {
	// A model claiming too_large or error is not trusted — those are the
	// plugin's to assert. Treat as unclear.
	host := stubHost{res: plugin.CallResult{StructuredContent: `{"verdict":"error","explanation":"nope"}`}}
	got := Review(context.Background(), host, ReviewInput{DocContent: "d", Diff: "x"})
	if got.Verdict != VerdictUnclear {
		t.Errorf("model-claimed error should be treated as unclear, got %q", got.Verdict)
	}
}

func TestReviewCallErrorIsError(t *testing.T) {
	host := stubHost{err: errors.New("host boom")}
	got := Review(context.Background(), host, ReviewInput{DocContent: "d", Diff: "x"})
	if got.Verdict != VerdictError {
		t.Errorf("a failed call should be error, got %q", got.Verdict)
	}
}

func TestPromptVersionStable(t *testing.T) {
	if PromptVersion == "" || len(PromptVersion) != 12 {
		t.Errorf("PromptVersion should be a 12-char hash, got %q", PromptVersion)
	}
}
