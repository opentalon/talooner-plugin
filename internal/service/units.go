package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opentalon/talooner-plugin/internal/facts"
)

// codeUnit is one touched, documented unit of code (a model/controller/service)
// the bot proposes for review. doc_content is read from the BASE branch by the
// bot, so a fork PR cannot rewrite the documentation it is judged against; diff
// is this unit's slice of the change. important gates the model call for token
// economy — a ruleset reviews only units the bot flagged as worth it.
type codeUnit struct {
	Name          string `json:"name"`
	Important     bool   `json:"important"`
	DocURL        string `json:"doc_url"`
	DocContent    string `json:"doc_content"`
	Diff          string `json:"diff"`
	DiffTruncated bool   `json:"diff_truncated"`
}

// parseCodeUnits decodes the evaluate_pr `code_units` arg (a JSON array). An
// empty arg is no units, not an error — most PRs touch nothing documented.
func parseCodeUnits(raw string) ([]codeUnit, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var units []codeUnit
	if err := json.Unmarshal([]byte(raw), &units); err != nil {
		return nil, fmt.Errorf("talooner: invalid code_units: %w", err)
	}
	return units, nil
}

// factSet is the code_unit record's facts. The review result attributes
// (unit.llm_result, unit.llm_explanation) are written by the enrich step, not
// here. The whole unit.* namespace is reserved from tenant assert_facts.
func (u codeUnit) factSet() facts.Set {
	return facts.Set{
		"unit.name":           u.Name,
		"unit.important":      u.Important,
		"unit.doc_url":        u.DocURL,
		"unit.doc_content":    u.DocContent,
		"unit.diff":           u.Diff,
		"unit.diff_truncated": u.DiffTruncated,
	}
}
