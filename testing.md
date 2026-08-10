# `talooner-plugin` — testing

The seam pays off here: the plugin takes facts and returns actions, so **every
test is a pure function test**. No GitHub, no runners, no fixtures of API JSON.

## Unit

Feed a fact set plus a ruleset, assert on the returned action list. No network.
The interesting cases are the ones that don't fire:

- a fact left unset mid-CI → rule gated on `== true` does not fire, appears in
  `explain`
- `not is "critical_path"` where `critical_path` is **unset** → the rule **does**
  fire. Pin the accepted A1 behaviour with a test so a future engine change
  surfaces as a failure here rather than as a wrong review
- a list-valued `pr.changed_files` against a `contains` predicate → **matches**
  when any element does
  ([`talon-language#158`](https://github.com/opentalon/talon-language/issues/158),
  landed 2026-08-07). Two cases beside it, both of which the fix leaves failing
  and neither of which is obvious from the rule text: an **empty**
  `pr.changed_files` matches nothing, and a glob-shaped `matches "**/*.css"`
  matches nothing because `matches` is a substring scan
- `assert_facts` writing `pr.tests_passing` → rejected. One case per forbidden
  namespace (`pr.*`, `user.*`, `repo.*`, `review.*`, `event.*`, `llm_review.*`),
  because since decision 1 this is the only filter in the system — the caller has
  no endpoint to filter at (`facts.md`)
- `assert_facts` on a permitted namespace → accepted, and the response carries
  **no** action list (decision 20)
- a fact asserted by CI, then a later `evaluate_pr` → the rule fires on that run,
  proving the store-only path still reaches a verdict
- `evaluate_pr` twice at the same sha, `force=false` → one LLM call;
  `force=true` → two, and the budget ceiling still refuses past the cap
- quota exhausted → `llm_review.result = "error"`, run completes, nothing approved
- tenant `approve` and tenant `block` both fire, unresolved → both returned plus a
  warning
- a `strict` base rule defeating a tenant `approve`
- a tenant ruleset that redefines a `strict` base rule by name → `validate_ruleset`
  fails with the name collision, naming the imported file
  ([`talon-language#159`](https://github.com/opentalon/talon-language/issues/159)).
  This is the one test standing between a tenant and deleting a safety rule
- ruleset using `do deploy_preview` → `validate_ruleset` fails with "unknown
  action". Also one per *misspelling* (`do aprove`): Talon validates no verb
  names at all, so this check is the only thing between a typo and an action
  that silently never happens

## Ruleset tests

Reuse `talon-language`'s `.tln.test` framework and `internal/testrunner`
directly. `validate_ruleset` and `talooner rules test` are the same code path —
that's deliberate, so a tenant's CI and the plugin can never disagree about
whether a ruleset is valid.

Assert on the actions a rule produced, not only on which rows it matched:

```talon
test "critical path tags the owner" {
  given {
    record 1 type "pr"
    attr 1 "pr.changed_files" ["internal/auth/session.go"]
    attr 1 "user.owner" "@alice"

    record 2 type "pr"
    attr 2 "pr.changed_files" ["README.md"]
    attr 2 "user.owner" "@bob"
  }

  when rule "Require human review for critical paths"

  expect {
    flagged 1
    did 1 require "review.senior_engineer"
    did 1 assign "pr" "@alice"
    did 1 comment "pr" contains "@alice"
    did_not 2 require "review.senior_engineer"
  }
}
```

`did_not` is the one that earns its keep — a rule firing on PRs it shouldn't is
invisible in a suite that only checks the PRs it should. `given` takes list
literals, including the empty list, which is how the `pr.changed_files` edges get
exercised. Full reference:
[`talon-language/docs/actions.md`](https://github.com/opentalon/talon-language/blob/main/docs/actions.md).

The base ruleset ships with its own `.tln.test`, and it is the tenant-facing
example: a repo that copies it gets a policy with tests already attached.

## VCR cassettes for `llm_review`

Per the core's convention. Editing a prompt `.txt` invalidates cassettes and
fails CI until re-recorded — see `opentalon/CLAUDE.md`.

## The determinism test

This one is the product, not a nicety: evaluate the same fact set twice and
assert **byte-identical actions and exactly one LLM call**. If that test can't be
made to pass, the central claim of the project is false.

Extend it to the retraction path: evaluate, mutate one fact, re-evaluate, and
assert the previously-fired action is absent from the second result.

Decision 18 leans on this test harder than before. `/review` now always
re-evaluates instead of re-rendering a stored verdict, so "same input, same
output, one LLM call" is what makes re-invocation free — if determinism is
broken, the visible symptom is a maintainer being billed for typing `/review`
twice.
