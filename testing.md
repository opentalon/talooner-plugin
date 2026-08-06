# `talooner-plugin` — testing

The seam pays off here: the plugin takes facts and returns actions, so **every
test is a pure function test**. No GitHub, no webhooks, no fixtures of API JSON.

## Unit

Feed a fact set plus a ruleset, assert on the returned action list. No network.
The interesting cases are the ones that don't fire:

- a fact left unset mid-CI → rule does not fire, appears in `explain`
- `not is "critical_path"` where `critical_path` is unknown → still unknown
- `assert_facts` writing `pr.tests_passing` → rejected
- quota exhausted → `llm_review.result = "error"`, run completes, nothing approved
- tenant `approve` and tenant `block` both fire, unresolved → both returned plus a
  warning
- a `strict` base rule defeating a tenant `approve`
- ruleset using `do deploy_preview` → `validate_ruleset` fails with "unknown
  action"

## Ruleset tests

Reuse `talon-language`'s `.talon.test` framework and `internal/testrunner`
directly. `validate_ruleset` and `talooner rules test` are the same code path —
that's deliberate, so a tenant's CI and the plugin can never disagree about
whether a ruleset is valid.

## VCR cassettes for `llm_review`

Per the core's convention. Editing a prompt `.txt` invalidates cassettes and
fails CI until re-recorded — see `opentalon/CLAUDE.md`.

## The determinism test

This one is the product, not a nicety: evaluate the same fact set twice and
assert **byte-identical actions and exactly one LLM call**. If that test can't be
made to pass, the central claim of the project is false.

Extend it to the retraction path: evaluate, mutate one fact, re-evaluate, and
assert the previously-fired action is absent from the second result.
