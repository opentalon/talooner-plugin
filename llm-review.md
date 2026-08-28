# `llm_review`

The only LLM call anywhere in Talooner, and it lives here because **the cluster
is the only component holding provider credentials**. The bot never sees a model.

Diagram: `diagrams.md` §5.

`llm_review` is **not a `do` verb**. The model is invoked through tln's native
`tool "llm" "review"` step inside an `enrich` block, resolved by this plugin's
`ToolResolver`. The engine calls the tool per matching record and writes the
verdict back onto that record; rules then react to it like any other fact:

```tln
enrich "Review touched code units against their docs" {
  for records where type == "code_unit" and attr "unit.important" == true
  stale_after 1 hour
  tool "llm" "review" {
    unit attr "unit.name"
    doc  attr "unit.doc_content"
    diff attr "unit.diff"
  }
  update attr "unit.llm_result"      from result.verdict
  update attr "unit.llm_explanation" from result.explanation
}

rule "Block on a documented mismatch" {
  for records where type == "code_unit" and attr "unit.llm_result" == "mismatch"
  do block "pr.merge"
  do comment "pr" "Code contradicts the module docs"
}
```

The **granularity is one review per code unit** (a model/controller/service),
not one per PR: each `code_unit` record carries its own doc mapping and diff
slice, and `unit.important` gates the call for token economy. The bot supplies
the code units on `evaluate_pr` (`code_units`, `facts.md`), reading each unit's
**documentation content from the base branch** — so a fork PR cannot rewrite the
thing it is judged against. The bot never sees a model.

### The call goes through the host, not a provider SDK

The cluster holds the credentials, but they live in the **OpenTalon host**, not
this plugin. So the plugin does not embed a provider SDK; its `ToolResolver`
asks the host to do the call:

- The plugin declares `SupportsCallbacks` and implements
  `StreamingHandler.ExecuteWithCallbacks`, so the host dispatches `evaluate_pr`
  over `ExecuteBidi` and hands it a live `HostCaller`.
- On each `tool "llm" "review"` step the resolver calls
  `host.RunAction("_subprocess", "run", {task, tools: "none", max_iterations: "1"})`
  — a bounded, single-turn, **tool-less** sub-agent (`tools: "none"`, so an
  injected "run the deploy tool" from the diff can't fire). The host runs it
  with the tenant's cluster credentials and returns the answer inline.
- Token spend is metered by the host (`opentalon_llm_*` per entity,
  `opentalon_plugin_*` per plugin/action), so a review is attributable to the
  calling repo/workflow without inspecting model output.

The resolver owns two things tln cannot express: it constrains the answer to the
fixed enum below, and it enforces the caps and the cache (next section).

**Standalone TCP mode has no host**, so no callback channel. There, `whoami`
withdraws `llm_review` from the tenant's features and a fired review degrades to
`result: "error"` — it never reaches a model that isn't there.

### Two passes, and why the cache lives in the resolver

tln executes blocks **unordered** and never re-evaluates rules after an `enrich`,
so a rule cannot see the verdict an enrich produced in the *same* run. So
`evaluate_pr` runs the engine **twice** over one fact store when there are code
units: pass 1 installs the resolver and the `enrich` step populates
`unit.llm_result` on each record; pass 2 runs with no resolver (enrich is a
no-op) and the rules read the now-present facts. Rulesets with no code units run
a single pass and pay nothing.

`stale_after` is tln's own freshness gate, but it **cannot be talooner's cache**:
the fact store is rebuilt per `evaluate_pr`, so write-times reset every run.
Determinism (decision 9/18 — "same head sha ⇒ one call, byte-identical") is
therefore the resolver's job. It caches each verdict keyed by
`(scope, head_sha, unit, prompt_version)`:

```
tool "llm" "review" fires for a code_unit
  → look up (scope, head_sha, unit, prompt_version)
      hit  → return it. No host call, no spend.     ← unless force=true
      miss → check the per-tenant quota
               exhausted → verdict "error", no call
               ok        → host runs the model → cache it → return it
```

A new commit means a new head sha means a cache miss means the model runs again.
`force` bypasses the hit but not the quota.

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
