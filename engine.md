# `talooner-plugin` — internals and decision-making

See `diagrams.md` §2b for the picture.

## Pipeline, in execution order

1. **gRPC surface** — implements `PluginService`, owns the proto. Decodes the
   fact JSON, validates the request shape. See `protocol.md`.
2. **Ruleset loader** — parse, validate, compile. Two rulesets are always loaded:
   the tenant's, and Talooner's own `strict` base ruleset, which the tenant's
   `import`s. See `OPEN-QUESTIONS.md` A5 for why this is `import` and not a
   string concatenation.
3. **Fact assertion** — facts land in `tln-db` under a per-PR scope. Facts
   absent from this request are *retracted*, not left stale; the bot always sends
   a full re-derivation, never a delta. This is what makes approval retraction
   work (`diagrams.md` §4). Details in `facts.md`.
4. **Engine** — `tln-language`'s RETE-ish reactive engine.
5. **`llm_review`** — invoked only when a rule fires it, and the one action this
   plugin performs rather than returns. Its results land as facts, so the engine
   runs a **second pass** when any fired; rules reading `llm_review.*` reach a
   verdict there. Bounded at two passes. See `llm-review.md`.
6. **Defeasible resolution** — `strict` > `overrides` > priority
   (`tln-language/docs/defeasible.md`). Not an ad-hoc "block wins" in Go.
7. **`explain` / audit** — persisted before returning, so a decision is queryable
   even if the caller never executes it. Under decision 1 that's not a rare crash
   case: the caller is a workflow run, and a cancelled job dies mid-execution
   without notice. The record has to exist before the response leaves.

## The base ruleset

Talooner ships rules it always loads, declared `strict` so a tenant ruleset can't
defeat them:

```tln
strict rule "Never approve a PR with unresolved conflicts" {
  for records where type == "pr"
    and attr "pr.mergeable" == false
  block "merge"
  do block "pr.merge"
  do comment "pr" "This PR has unresolved conflicts — resolve them before review"
  priority CRITICAL
}

strict rule "Never approve while required checks are still running" {
  for records where type == "pr"
    and attr "pr.checks_pending" == true
  block "merge"
  do block "pr.merge"
  priority CRITICAL
}
```

Phase 0 answered the question this design rested on. `overrides`/priority
resolution works across a combined base + tenant ruleset
(`tln-language/internal/defeasible/defeasible.go:33`); `overrides` across two
*separately compiled* programs is a compile error, so the two are loaded as one
program via `import`.

The hole was elsewhere and is closed: a tenant block redefining an imported name
used to silently replace it, which deleted the `strict` rule it was named after.
Since [`tln-language#159`](https://github.com/opentalon/tln-language/issues/159)
(landed 2026-08-07) that is a compile error naming the imported file and line, so
the tenant sees it at `validate_ruleset` time and renames.

## Abstract action vocabulary

Rules declare actions with `do <verb> <args>`
([`tln-language/docs/actions.md`](https://github.com/opentalon/tln-language/blob/main/docs/actions.md)).
The engine decides which fire and resolves their arguments against the matched
row; the plugin returns them as data. It does not know what any of them mean on
GitHub — that translation is the bot's, and the GitHub semantics live in
[`talooner/actions.md`](https://github.com/opentalon/talooner/blob/main/actions.md).

| Verb | Args | What the plugin guarantees |
|---|---|---|
| `approve` | target (`"pr"`) | Fired only when no `strict` rule defeated it |
| `block` | target (`"pr.merge"`) | — |
| `comment` | target, text | `{attr.x}` interpolation already resolved against facts |
| `assign` | target, team-or-user | `attr "user.owner"` arrives resolved to a value, not as the string `"user.owner"` |
| `require` | `review.<target>` | — |
| `notify` | target, text | The bot dispatches through an OpenTalon channel |
| `emit` | name | Asserts fact `event.<name> = true` in the scope; no external effect |

### The verb list is ours to enforce

**`tln-language` does not validate verb names.** The verb vocabulary belongs
to the host by design — a review bot's `approve` and a fleet system's `dispatch`
are the same construct — so the language parses `do anything_at_all "x"` without
complaint and hands it back.

That makes `validate_ruleset` the only thing standing between a typo and a
silently dropped action. It must reject any verb outside the seven above, name
the offending verb, and list the valid set. An unknown verb reaching the bot is
a ruleset bug; an unknown verb reaching *nothing* is the failure mode worth
ruling out, because it looks exactly like a rule that didn't match.

Two further constraints this table encodes:

- **`reject` is not in the vocabulary.** "Request changes" is how `block` renders
  on GitHub, not a separate verb. Adding a verb here means adding a matching
  executor file in the bot repo, and keeping the two sets identical is what stops
  them drifting.
- **`deploy_preview` / `screenshot` / `scan_dependencies` are not verbs.** They
  parse — `do deploy_preview "pr"` is valid Tln — so `validate_ruleset`
  rejects them by name with a pointer to the facts API. The tenant's CI does that
  work and asserts the result via `assert_facts`; rules react. Better than
  accepting the verb and doing nothing.

Interpolation in action-argument position and fact references as action
arguments were both phase-0 verification items and both came back ✅, now with
real syntax behind them: `do assign "pr" attr "user.owner"` passes the resolved
value, and `"…{attr.user.owner}…"` interpolates. One wrinkle survives —
`{item.<field>}` resolves only for `item.name`, so use `{id}` / `{attr.x}`.

## Conflict resolution — defeasible

`approve` and `block` can both fire. Resolved by Tln's defeasible machinery
(`tln-language/docs/defeasible.md`), not by an ad-hoc "block wins" in Go:

- Safety rules are declared `strict` — they always fire, never defeated.
- Priority ordering `CRITICAL > HIGH > MEDIUM > LOW`, default `MEDIUM`.
- `overrides "Rule name"` for explicit defeat, walked transitively.
- An unresolved tie fires both and warns.

An unresolved tie between a tenant `approve` and a tenant `block` resolves
conservatively: both actions are returned, and a ruleset warning goes back in
`warnings[]` telling the maintainer to disambiguate with `overrides` or
`priority`. The bot's check-run writer applies block-wins as a **last-resort**
tiebreak so the check run has one value. The warning is the real product; the
tiebreak is just so something can be written.

## `explain` and audit

Every decision persists: facts at evaluation time, ruleset content hash, rules
that fired, rules suppressed by defeasible resolution, `explain` output, actions
returned. Persisted *before* the response goes back, so a caller dying
mid-execution — a cancelled workflow run, a runner timeout — still leaves a
queryable record.

Rules that didn't fire because a condition was unmet appear in `explain` too —
"would have approved but `tests_passing` was unset" is the single most useful
line the system can print, and under the A1 semantics it's the *only* signal that
distinguishes an unset fact from a false one. See `facts.md`, "Unset is false".

`explain_pr` renders it for a given head sha; that's what backs `@talooner /why`.
Determinism plus a stored explanation means "why did the bot block this?" has an
exact answer, which is the whole reason for not putting a model in the decision
path.

## Determinism

Same head sha + same facts + same ruleset ⇒ byte-identical actions. Holds
because:

- Conflict resolution is defeasible, not load-order dependent.
- `llm_review` results are stored as facts keyed by
  `(pr, head_sha, doc_url, prompt_version)`, so a re-run at the same sha reads
  the stored fact instead of calling the model.
- The per-PR LLM conversation informs an answer; it never changes an answer
  already recorded.

This is the product, so it gets a test — see `testing.md`.
