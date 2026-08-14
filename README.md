# talooner-plugin

The server side of [Talooner](https://github.com/opentalon/talooner) — everything
that runs **inside the OpenTalon cluster**.

The plugin receives extracted facts and ruleset text and returns a decision. It
never touches GitHub, never calls out, never holds a GitHub credential.

Module: `github.com/opentalon/talooner-plugin`.

> **Terminology.** These docs say "the bot" for Talooner's GitHub half. Since
> decision 1 that half is a **GitHub Action** running in the tenant's own Actions
> runner — an ephemeral process that starts on an event, calls this plugin, and
> exits. It is not a service. Anywhere the distinction matters (crash recovery,
> connection reuse, who holds what credential), these docs say so explicitly.

> **Status: design phase.** This repo currently contains design documents only —
> no code, no proto, nothing runnable. Everything here is subject to change until
> phase 1 lands. See `roadmap.md`.

## Responsibility

| Owns | Does not own |
|---|---|
| Ruleset parse / validate / compile | Anything GitHub-shaped |
| Fact assertion and per-PR scoping in `tln-db` | Fact *extraction* — that's the bot |
| Tln engine execution, reactive rules | Executing actions — it returns them as data |
| Defeasible conflict resolution | Deciding what a GitHub check run is |
| `llm_review` — the only LLM call in the system | Any GitHub credential |
| `explain` / audit persistence | Triggering — that's a workflow event |
| Subscription state | Rate limits against GitHub |
| **All** state, because the caller has none | Initiating anything on its own |

The seam is deliberate: the plugin returns an **abstract action list** and the
bot translates it into API calls. Consequence for whoever builds this — the
entire plugin is testable with zero GitHub fixtures. Feed it facts, assert on
returned actions.

## Docs

| File | Contents |
|---|---|
| `diagrams.md` | **Start here** — placement in the cluster, internals, the flows this component participates in |
| `protocol.md` | The OpenTalon plugin contract: actions to declare, `user_only`, payload sizing |
| `engine.md` | Internals in execution order, the `strict` base ruleset, defeasible conflict resolution, the abstract action vocabulary |
| `facts.md` | Namespaces, per-PR scoping and lifetime, namespace enforcement, three-valued semantics, retention |
| `llm-review.md` | The only LLM call anywhere in Talooner, and why the fact store is the cache |
| `deployment.md` | Running it in a cluster, and the `tln-db` dependency chain — read before the first build |
| `testing.md` | Unit, ruleset, VCR, and the determinism test |
| `roadmap.md` | Plugin-scoped phases, and the phase-0 substrate this depends on |
| `OPEN-QUESTIONS.md` | Phase-0 findings and the calls made on them |

## Decisions inherited from the ecosystem

The full list lives in [`talooner/README.md`](https://github.com/opentalon/talooner).
The ones that constrain this repo:

| # | Decision |
|---|---|
| 1 | **The GitHub half is an Action, not a hosted App.** It runs in the tenant's runner, lives for one job, and calls this plugin over gRPC from outside the tenant's network. Three consequences here: the cluster must be reachable from a runner, nothing can be cached in the caller between events, and this plugin holds *all* the state there is. |
| 2 | **Thin stateless caller + `talooner-plugin` in an OpenTalon cluster.** The action knows GitHub and nothing about Tln; the plugin knows Tln and nothing about GitHub. |
| 3 | **Self-hosted. Forever.** The cluster holds the tenant's LLM credentials; the tenant pays for their own tokens. |
| 5 | **Facts live in `tln-db`**, per PR, persistent — required for reactive rules. |
| 7 | **Defeasible conflict resolution**, not ad-hoc "block wins". |
| 9 | **No LLM cache layer** — `llm_review` results are facts keyed by head sha, which is the cache. |
| 11 | **No dispatch actions.** The tenant's CI does the work and POSTs the result to the facts API; rules react. |
| 12 | **Two repos.** `talooner` (action + CLI) and `talooner-plugin` (engine, fact store, proto). Separate concepts, separate versions. |
| 18 | **`/review` always re-evaluates.** Decision 9 already makes that free at an unchanged sha. `--force` maps to a cache-bypass arg on `evaluate_pr`, and the per-tenant budget ceiling still applies to it. |
| 20 | **No reactive wake in v1.** Nothing is running between events, so an `assert_facts` call cannot cause a GitHub write. Facts land in the store and are read by the next `evaluate_pr`. |

The shared proto lives here, since the plugin is the server and owns the
contract. The bot imports the generated Go package as a normal tagged dependency
— the same relationship `mcp-plugin` has with `opentalon`. Landing order for a
contract change: plugin first, tag, then bump the bot.

## First task

Write the example ruleset from
[`talooner/README.md`](https://github.com/opentalon/talooner) as a `.tln` +
`.tln.test` in `tln-language/examples/`, running on synthetic facts, no
GitHub involved. This is phase 0's exit criterion, it costs about a day, and as
of 2026-08-07 nothing blocks it — the three substrate fixes it was waiting on
have landed (`OPEN-QUESTIONS.md`). If it can't be written, the design is wrong
and that's worth knowing before any plugin code exists.

Two things it will hit that the design already accounts for: `matches` is a
substring scan rather than a glob (write path predicates with `contains` /
`ends_with`), and the base ruleset is `import`ed rather than concatenated.

## Related repos

| Repo | Role |
|---|---|
| [`talooner`](https://github.com/opentalon/talooner) | The GitHub Action + CLI. Consumes this plugin's contract |
| [`opentalon`](https://github.com/opentalon/opentalon) | Core orchestration platform and plugin host |
| [`tln-language`](https://github.com/opentalon/tln-language) | The Tln DSL: grammar, parser, inference engine, `.tln.test` |
| [`tln-db`](https://github.com/opentalon/tln-db) | Embedded fact store backing Tln |

## Contributing

Design phase, so the highest-value contribution right now is disagreement.
`OPEN-QUESTIONS.md` records the phase-0 findings and the calls made on them —
including one accepted risk (A1: unset facts read as false, so a failed
extraction can approve) that is worth arguing with if you think it's wrong.

## License

Apache-2.0. See `LICENSE`.
