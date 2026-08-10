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
| custom (`preview.*`, `screenshots.*`, `dependency_scan.*`, …) | pushed by tenant CI | until PR closes | yes — store-only, read at the next `evaluate_pr` |

## Scoping and lifetime

One scope per `(repo, pr)`. Contents:

- Facts asserted by the bot each run (`pr.*`, `user.*`, `review.*`, …)
- Facts pushed by tenant CI (`preview.*`, `screenshots.*`, …)
- `llm_review.*` results, keyed by head sha
- Subscription state — a fact like everything else (`OPEN-QUESTIONS.md` B1), so
  `when attr "pr.subscribed" == true` is expressible and there's one storage story
- Decision + `explain` records

"Scope" is a Talooner concept, not a `talon-db` one. The store is keyed
`(entity_id, doc_id)` with `entity_id` pinned to one tenant per client, so a
scope is **one document per PR**, keyed `{repo}#{number}` (`OPEN-QUESTIONS.md`
A7). Two consequences that leak upward: scoping an evaluation to a single PR
means injecting a `pr_key` pattern into every selector at load time — invisible
to tenant rule authors, but it has to happen or every rule sees every PR's
records — and there is no drop-scope call, only per-document deletes.

**Full re-derivation, never deltas.** Every `evaluate_pr` carries the complete
fact set at that head sha. Facts absent from the request are *retracted*. This is
what makes retraction work: a PR that was approved and then grew past 500 lines
has the approving fact set replaced, the rule stops firing, and the bot dismisses
the review (`diagrams.md` §4).

Retention: facts expire after a grace period once the PR closes; decisions and
`explain` outlive them, because "why did the bot block this?" gets asked months
later. Defaults are 90 days for facts, forever for decisions, both configurable
(`OPEN-QUESTIONS.md` B2). Note that `talon-db` has no bulk delete, so this is a
sweeper job phase 2 owes — `Scan` plus a per-doc `Delete` — not a config key
someone sets.

Decision 20 puts a floor under the facts number. An externally asserted fact sits
unread until someone runs `/review`, so retention shorter than "nobody touched
this PR for a few days" silently drops facts that were never used.

## Namespace enforcement lives here

`assert_facts` must reject writes to `pr.*`, `user.*`, `repo.*`, `review.*`,
`event.*` and `llm_review.*`.

Without that check, a tenant's CI workflow can POST `pr.tests_passing: true` and
defeat the entire ruleset.

This got stricter under decision 1. Earlier drafts had two independent checks —
the bot filtered at its own facts API, the plugin filtered again at the store.
There is no bot endpoint any more; CI POSTs directly to the cluster. **This is
the only check that exists.** It is load-bearing for every rule anyone writes,
and it deserves tests that try each forbidden namespace explicitly rather than a
single happy-path case.

### `assert_facts` is store-only in v1

Asserting a permitted fact does **not** produce a GitHub effect. Decision 20: the
caller is a workflow run that exited long before the tenant's CI POSTed anything,
and this plugin holds no GitHub credential, so a woken rule has nobody to act on
its actions.

So the v1 contract is: `assert_facts` validates the namespace, writes to the
scope, returns what it accepted and rejected. The fact enters a verdict at the
next `evaluate_pr` — triggered by a human typing `@talooner /review`, or by the
next push. This is still the mechanism behind every v2 rule (Talooner doesn't
build preview environments, it reacts to a fact saying one exists); it just isn't
prompt.

Whether an out-of-band assertion *could* wake the reactive engine was phase-0
item A6, dropped by decision 20 and deferred to phase 4 (`roadmap.md`) — the
answer only matters once there's a dispatch-triggered run alive to act on the
wake.

## Unset is false, and that asymmetry is load-bearing

The single most dangerous detail in the system. Phase 0 settled it, and not the
way this document originally assumed.

`talon-language`'s evaluator is **two-valued**, with closed-world
negation-as-failure. There is no `unknown`. A missing attribute makes its pattern
fail, which makes any enclosing `not` *succeed*
(`internal/factstore/memory.go:691,773`; `talon-db/bboltstore/query.go:314` —
both backends agree). Probed directly; see `OPEN-QUESTIONS.md` A1.

The consequence splits by condition shape:

| Condition | Fact unset | Safe? |
|---|---|---|
| `attr "pr.tests_passing" == true` | doesn't match, rule doesn't fire | yes |
| `not is "critical_path"` | **matches, rule fires** | **no** |

Positive conditions fail closed. Negated conditions fail open: a rule shaped
`not is "critical_path"` reads a failed extraction as "not on the critical path"
and approves.

**v1 accepts this.** The decision, its caveats, and the `strict` +
`pr.facts_complete` guard that would close it — deliberately not built — are in
`OPEN-QUESTIONS.md` A1. This is a property of the engine, not something the
plugin implements around; the plugin's obligation is to not paper over it, which
means `explain` output must make a non-firing rule's reason visible.

The unset case is not exotic. The bot deliberately leaves `pr.tests_passing`
unset while CI is still running, and unset when no check matches the tenant's
patterns at all. Both are safe, because the rules that read it are gated on
`== true`.

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
  attr "pr.changed_files" contains "internal/auth/"
    or attr "pr.changed_files" contains "app/models/user.rb"
}
```

`grammar.ebnf:515` specifies `contains | starts_with | ends_with | matches`
against a **string** operand. For this to work, `contains` has to mean "any
element contains" — existential quantification over the list.

**It does, since 2026-08-07.** Phase 0 found it didn't: both evaluator paths
type-asserted their operands to `string` and returned false for a list with no
diagnostic. Fixed generally in `talon-language` rather than special-cased here —
[`talon-language#158`](https://github.com/opentalon/talon-language/issues/158),
landed in `talon-language` 35109f0 and `talon-db` e1c8ddb. The two layers now
agree: `talon-db/internal/index/terms.go:124` indexes each array element as its
own inverted term so candidate gathering finds the document, and the verify step
no longer rejects it.

The separator-delimited `pr.changed_paths_joined` fallback is dropped — it was
only ever the plan if the fix stalled, and it leaked sentinels into tenant rules.

Two edges tenant rules hit, both worth a `validate_ruleset` lint later:

- **A list with no string elements matches nothing.** No fallback to the scalar
  path, so an empty `pr.changed_files` fails every predicate. Right direction:
  under A1 semantics a `not`-shaped rule over it still fails open, which is why
  the extractor asserts `pr.changed_files` even when the list is empty.
- **`matches` is a substring scan, not a glob.** Case-insensitive and contiguous
  locally; term-AND on Datalevin. `matches "**/*.css"` matches nothing — no path
  contains that text. Path predicates use `contains` and `ends_with`.

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
`when attr "module.touched_count" > 1`.
