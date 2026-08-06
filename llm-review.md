# `llm_review`

The only LLM call anywhere in Talooner, and it lives here because **the cluster
is the only component holding provider credentials**. The bot never sees a model.

Diagram: `diagrams.md` §5.

```
rule fires llm_review(doc_url, diff)
  → look up fact (pr, head_sha, doc_url, prompt_version)
      hit  → return it. No API call, no spend.
      miss → call the model → store result as a fact → return it
```

The fact store *is* the cache. No separate cache layer, no invalidation logic: a
new commit means a new head sha means the fact is absent means the model runs
again.

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
  approve. Remaining quota is surfaced through `whoami` so the bot can warn at
  ruleset-load time rather than failing on the first PR.
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

1. The bot loads the governing ruleset from the **base** branch, always, and
   gives head-branch rulesets a read-only plan run (`diagrams.md` §6).
2. The per-PR cap and per-tenant ceiling here apply regardless of which branch
   the ruleset came from.
