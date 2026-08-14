package service

import (
	"fmt"
	"strconv"

	"github.com/opentalon/opentalon/pkg/plugin"

	"github.com/opentalon/talooner-plugin/proto/taloonerpb"
)

// explainPR renders the persisted explanation for a PR's decision at a given
// head sha (read-only). It backs `@talooner /why`. Determinism plus a stored
// explanation is what gives "why did the bot block this?" an exact answer — the
// whole reason a model stays out of the decision path.
//
// A sha that was never evaluated is a distinct, clear error — not an empty
// explanation that would read like "no rules fired". Because the decision
// outlives the facts, this still renders after retention has swept them.
func (s *Server) explainPR(req plugin.Request) plugin.Response {
	repo := req.Args["repo"]
	if repo == "" {
		return errorResponse(req, fmt.Errorf("talooner: repo is required"))
	}
	prNumber, err := strconv.Atoi(req.Args["pr"])
	if err != nil {
		return errorResponse(req, fmt.Errorf("talooner: invalid pr %q: must be a number", req.Args["pr"]))
	}
	headSHA := req.Args["head_sha"]
	if headSHA == "" {
		return errorResponse(req, fmt.Errorf("talooner: head_sha is required"))
	}

	d, ok := s.decision(repo, prNumber, headSHA)
	if !ok {
		return errorResponse(req, fmt.Errorf(
			"talooner: no decision recorded for %s#%d at %s; it was not evaluated at that sha",
			repo, prNumber, headSHA))
	}

	resp := &taloonerpb.ExplainPrResponse{Explain: d.Explain}
	summary := ""
	if d.Explain != nil {
		summary = d.Explain.Summary
	}
	return structuredResponse(req, resp, summary)
}
