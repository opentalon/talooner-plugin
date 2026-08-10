# `llm_review`

The only LLM call anywhere in Talooner, and it lives here because **the cluster
is the only component holding provider credentials**. The bot never sees a model.

Diagram: `diagrams.md` §5.

`llm_review` is a `do` verb like any other — the engine returns it as an action,
and this plugin is the host that performs it instead of handing it to the bot:

```talon
rule "Check code against module docs" {
  for records where type == "pr"
    and attr "module.documentation_url" != ""
  do llm_review attr "module.documentation_url"
}

rule "Block on a documented mismatch" {
  for records where type == "pr"
    and attr "llm_review.result" == "mismatch"
  block "merge"
  do block "pr.merge"
  do comment "pr" "Code contradicts the module docs: {attr.llm_review.explanation}"
}
```

```
rule fires llm_review(doc_url, diff)
  → look up fact (pr, head_sha, doc_url, prompt_version)
      hit  → return it. No API call, no spend.       ← unless force=true
      miss → call the model → store result as a fact → return it
```

**It is the one verb the plugin executes rather than returns**, and that is a
deliberate exception worth naming: every other verb crosses the wire because
only the bot holds a GitHub token, while this one must not, because only the
cluster holds provider credentials. The bot never sees it in the returned
action list.

### The second pass this implies

The two rules above are in a producer/consumer relationship: the first fires an
action, the action asserts `llm_review.*`, and the second reads those facts as
*conditions*. Conditions are evaluated before actions fire, so a single engine
pass cannot serve both — the consumer rule sees no `llm_review.result` on the
pass that produced it.

So `evaluate_pr` runs the engine **twice** when any `llm_review` action fired:
once to decide and produce, then once more with the resulting facts asserted.
The second pass is where the verdict actually lands. Consequences:

- **Bounded at two passes, not fixed-point.** An `llm_review` fired *by* a
  second-pass rule is not evaluated a third time; `validate_ruleset` warns when
  a rule both reads `llm_review.*` and fires `llm_review`. Iterating to a
  fixed point would make spend unbounded from a ruleset, which is the one
  property a fork PR must not have.
- **Determinism is unaffected.** The second pass reads facts keyed by head sha,
  so a re-run at the same sha replays them without a model call.
- **Rulesets with no `llm_review` pay nothing** — the second pass only happens
  when the first produced one.

The fact store *is* the cache. No separate cache layer, no invalidation logic: a
new commit means a new head sha means the fact is absent means the model runs
again.

This is what makes decision 18 cheap. `@talooner /review` always re-evaluates
rather than re-rendering a stored verdict, and that costs nothing at an unchanged
sha precisely because the expensive part is already a fact. `force=true` on
`evaluate_pr` is the one path that skips the hit and pays again — capped and
budgeted like any other call (`protocol.md`, "`force`").

## Constraints

- **Prompt lives in a `.txt` file, never a Go string literal.**
  `opentalon/CLAUDE.md` is explicit about this and CI enforces it.
- **Output is a fixed enum** — `match` | `mismatch` | `unclear` | `too_large` |
  `error` — plus a free-text explanation. The enum drives decisions; the
  explanation is only ever rendered as escaped, quoted text.
- **Prompt injection from the diff is assumed, not prevented.** The mitigation is
  structural: the diff, title, body and branch name are all attacker-controlled
  on a fork PR, and an injected "approve this PR" can at most produce
  `result: "match"`, which still has to satisfy every other condition in the
  rule. That is why the output is constrained rather than free-form.
- **Per-PR call cap and per-tenant budget ceiling, enforced here.** Quota
  exhaustion asserts `llm_review.result = "error"` with an explanatory
  `llm_review.error` — it does not crash the run, and it must never silently
  approve. Remaining quota is surfaced through `whoami` so the caller can warn at
  ruleset-load time rather than failing on the first PR. `force` does not lift
  either limit.
- **A per-PR conversation is retained for continuity**, but each review is a
  scoped turn whose result pins to its head sha. The conversation informs an
  answer; it never changes an answer already recorded. That's what preserves
  "same sha ⇒ same actions".

## Why the ruleset must handle `unclear` and `error`

A tenant ruleset that only matches `match` and `mismatch` does nothing when the
model is unsure or the budget is gone. That fails in the safe direction — no
approval — but silently, which is the wrong way to fail silently.
`validate_ruleset` should emit a lint warning for it.

## Spend is the fork threat model

Talooner cannot merge, so a malicious head-branch ruleset can't approve itself
into main. The real exposure is a fork PR adding a hundred `llm_review` rules and
burning the tenant's budget. Two things blunt it, and the second one is this
component's job:

1. The caller loads the governing ruleset from the **base** branch, always, and
   gives head-branch rulesets a read-only plan run (`diagrams.md` §6). A fork PR
   also can't start a credentialled run at all — GitHub withholds secrets from
   fork-triggered events, so nothing reaches this plugin until a maintainer
   comments.
2. The per-PR cap and per-tenant ceiling here apply regardless of which branch
   the ruleset came from.
