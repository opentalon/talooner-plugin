# `talooner-plugin` — facts

The plugin owns the fact **store**, the **scope**, and the **semantics**. It does
not own extraction — the bot produces `pr.*`, `user.*`, `repo.*` and `review.*`
from the GitHub API and sends them as a JSON blob on every `evaluate_pr`.

The full extracted vocabulary (every `pr.*` field, how `tests_passing` is derived
from check runs, CODEOWNERS resolution for `user.*`) is documented bot-side in
[`talooner/facts.md`](https://github.com/opentalon/talooner/blob/main/facts.md).
Read it once; this file covers what happens to those facts after they arrive.

## Namespaces

| Namespace | Source | Lifetime | Writable via `assert_facts`? |
|---|---|---|---|
| `pr.*` | GitHub API + diff, extracted by the bot | per PR, re-asserted each run | **no** |
| `user.*` | CODEOWNERS + `modules.yaml` + GitHub | per PR run | **no** |
| `repo.*` | repo config / GitHub metadata | per PR run | **no** |
| `review.*` | `pull_request_review` events | per PR, accumulates | **no** |
| `llm_review.*` | this plugin, from an LLM call | pinned to head sha | **no** |
| `module.*`, `team.*` | tenant-supplied lookup tables | static per repo | no |
| `event.*` | `do emit <name>` | per evaluation | no |
| custom (`preview.*`, `screenshots.*`, `dependency_scan.*`, …) | pushed by tenant CI | until PR closes | yes |

## Scoping and lifetime

One scope per `(repo, pr)`. Contents:

- Facts asserted by the bot each run (`pr.*`, `user.*`, `review.*`, …)
- Facts pushed by tenant CI (`preview.*`, `screenshots.*`, …)
- `llm_review.*` results, keyed by head sha
- Subscription state
- Decision + `explain` records

**Full re-derivation, never deltas.** Every `evaluate_pr` carries the complete
fact set at that head sha. Facts absent from the request are *retracted*. This is
what makes retraction work: a PR that was approved and then grew past 500 lines
has the approving fact set replaced, the rule stops firing, and the bot dismisses
the review (`diagrams.md` §4).

Retention: facts expire after a grace period once the PR closes; decisions and
`explain` outlive them, because "why did the bot block this?" gets asked months
later. Defaults are placeholders — see `OPEN-QUESTIONS.md` B5.

## Namespace enforcement lives here

`assert_facts` must reject writes to `pr.*`, `user.*`, `repo.*`, `review.*`,
`event.*` and `llm_review.*`.

Without that check, a tenant's CI workflow can POST `pr.tests_passing: true` and
defeat the entire ruleset. The bot also filters at its facts API, but the plugin
is the last line and the one that owns the store. Two independent checks, because
this one is load-bearing for every rule anyone writes.

Asserting a permitted fact wakes the engine, so reactive rules
(`when "preview.status" == "deployed"`) fire. This is the mechanism behind every
v2 action: Talooner doesn't build preview environments, it reacts to a fact
saying one exists. Whether an out-of-band assertion actually wakes the reactive
engine is phase-0 item A6 — if it doesn't, `assert_facts` does nothing and no
preview/screenshot/scan rule ever fires.

## Unset is not false

The single most dangerous detail in the system.

A condition on an **unset** fact must evaluate to *unknown*, not false. If unset
coerces to a zero value, the inverse pattern (`not is "critical_path"` where
`critical_path` failed to compute) silently classifies a critical PR as safe and
auto-approves it.

Rules:

1. A condition on an unset fact evaluates to **unknown**, not false.
2. A rule with any unknown condition **does not fire**.
3. `not is "critical_path"` where `critical_path` is unknown is **also
   unknown** — negation of unknown is unknown, not true.
4. Rules that didn't fire due to unknowns appear in `explain` output, so a
   maintainer can see "would have approved but `tests_passing` was unset".

This is a property of `talon-language`'s evaluator, not something the plugin can
implement around. Confirm points 1–3 against the actual executor before relying
on them; if it's two-valued, that's a prerequisite change in `talon-language`,
and it's the first thing phase 0 verifies. Hard blocker.

The unset case is not exotic. The bot deliberately leaves `pr.tests_passing`
unset while CI is still running, and unset when no check matches the tenant's
patterns at all — so a rule that auto-approves on `"pr.tests_passing" == true`
must not fire mid-CI, and must not fire on a repo with no tests.

## `llm_review.*`

| Fact | Type |
|---|---|
| `llm_review.result` | enum: `match` \| `mismatch` \| `unclear` \| `too_large` \| `error` |
| `llm_review.explanation` | string |
| `llm_review.doc_url` | string |
| `llm_review.error` | string, set only when `result == "error"` |

Keyed by `(pr, head_sha, doc_url, prompt_version)`. Rules must handle `unclear`
and `error`; a ruleset that only matches `match` and `mismatch` silently does
nothing on failure, which is the safe direction but should produce a lint warning
from `validate_ruleset`. See `llm-review.md`.

## List operands

`pr.changed_files` is a list, and every `pr.touches_*` predicate a tenant writes
is a `define` over it:

```talon
define "pr.touches_auth" {
  "pr.changed_files" contains "internal/auth/"
    or "pr.changed_files" matches "**/payment*"
}
```

`grammar.ebnf:515` specifies `contains | starts_with | ends_with | matches`
against a **string** operand. For this to work, `contains` must mean "any element
contains" and `matches` "any element matches" — existential quantification over
the list. Phase-0 item A2. If the executor doesn't do this, fix it generally in
`talon-language/internal/executor` rather than special-casing Talooner; the
fallback (have the bot also assert a newline-joined `pr.changed_paths_joined`) is
ugly enough to be a last resort.

## Cardinality: one evaluation per PR

A PR touching five modules is evaluated **once**, not five times. `module.*` binds
to the **primary** touched module: the one with the most changed lines, ties
broken by path order for determinism.

Re-running the ruleset per touched module would multiply every `llm_review` by
the number of touched modules and make "did the bot approve?" a fold over N
results instead of one answer. Not worth it.

The cost, so it isn't a surprise later: a PR that changes `auth/` heavily and
`billing/` slightly only ever checks its diff against the `auth/` docs.
`module.documentation_urls` (list) and `module.touched_count` are both asserted
so a ruleset can compensate — a future `llm_review` variant could take the list,
and a strict tenant can require narrow PRs with
`when "module.touched_count" > 1`.
