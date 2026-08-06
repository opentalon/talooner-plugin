# `talooner-plugin` — internals and decision-making

See `diagrams.md` §2b for the picture.

## Pipeline, in execution order

1. **gRPC surface** — implements `PluginService`, owns the proto. Decodes the
   fact JSON, validates the request shape. See `protocol.md`.
2. **Ruleset loader** — parse, validate, compile. Two rulesets are always loaded:
   the tenant's, and Talooner's own `strict` base ruleset.
3. **Fact assertion** — facts land in `talon-db` under a per-PR scope. Facts
   absent from this request are *retracted*, not left stale; the bot always sends
   a full re-derivation, never a delta. This is what makes approval retraction
   work (`diagrams.md` §4). Details in `facts.md`.
4. **Engine** — `talon-language`'s RETE-ish reactive engine.
5. **`llm_review`** — invoked only when a rule fires it. See `llm-review.md`.
6. **Defeasible resolution** — `strict` > `overrides` > priority
   (`talon-language/docs/defeasible.md`). Not an ad-hoc "block wins" in Go.
7. **`explain` / audit** — persisted before returning, so a decision is queryable
   even if the bot crashes before executing it.

## The base ruleset

Talooner ships rules it always loads, declared `strict` so a tenant ruleset can't
defeat them:

```talon
strict rule "Never approve a PR with unresolved conflicts" { ... }
strict rule "Never approve while required checks are still running" { ... }
```

Phase-0 open item: does `overrides`/priority resolution work correctly across
*two separately loaded* rulesets? If it doesn't, this design needs a change in
`talon-language/internal/defeasible`.

## Abstract action vocabulary

The plugin returns actions as data. It does not know what any of them mean on
GitHub — that translation is the bot's, and the GitHub semantics live in
[`talooner/actions.md`](https://github.com/opentalon/talooner/blob/main/actions.md).

| Verb | Args | What the plugin guarantees |
|---|---|---|
| `approve` | target (`"pr"`) | Fired only when no `strict` rule defeated it |
| `block` | target (`"pr.merge"`) | — |
| `comment` | target, text | Interpolation (`{ident.field}`) already resolved against facts |
| `assign` | target, team-or-user | The argument may be a *fact reference* (`"user.owner"`), resolved here |
| `require` | `review.<target>` | — |
| `notify` | target, text | The bot dispatches through an OpenTalon channel |
| `emit` | name | Asserts fact `event.<name> = true` in the scope; no external effect |

Two constraints this table encodes:

- **`reject` is not in the vocabulary.** "Request changes" is how `block` renders
  on GitHub, not a separate verb. Adding a verb here means adding a matching
  executor file in the bot repo, and keeping the two sets identical is what stops
  them drifting.
- **`deploy_preview` / `screenshot` / `scan_dependencies` are not verbs.** A
  ruleset using them fails `validate_ruleset` with "unknown action" and a pointer
  to the facts API. The tenant's CI does that work and asserts the result via
  `assert_facts`; rules react. Better than accepting the verb and doing nothing.

Interpolation in action-argument position (`grammar.ebnf:601`) and fact
references as action arguments are both phase-0 verification items — see
`roadmap.md`.

## Conflict resolution — defeasible

`approve` and `block` can both fire. Resolved by Talon's defeasible machinery
(`talon-language/docs/defeasible.md`), not by an ad-hoc "block wins" in Go:

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
returned. Persisted *before* the response goes back, so a bot crash mid-execution
still leaves a queryable record.

Rules that didn't fire because a condition was unknown appear in `explain` too —
"would have approved but `tests_passing` was unset" is the single most useful
line the system can print. See `facts.md`, "Unset is not false".

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
