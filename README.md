# talooner-plugin

The server side of [Talooner](https://github.com/opentalon/talooner) — everything
that runs **inside the OpenTalon cluster**.

The plugin receives extracted facts and ruleset text and returns a decision. It
never touches GitHub, never sees a webhook, never holds a GitHub credential.

Module: `github.com/opentalon/talooner-plugin`.

> **Status: design phase.** This repo currently contains design documents only —
> no code, no proto, nothing runnable. Everything here is subject to change until
> phase 1 lands. See `roadmap.md`.

## Responsibility

| Owns | Does not own |
|---|---|
| Ruleset parse / validate / compile | Anything GitHub-shaped |
| Fact assertion and per-PR scoping in `talon-db` | Fact *extraction* — that's the bot |
| Talon engine execution, reactive rules | Executing actions — it returns them as data |
| Defeasible conflict resolution | Deciding what a GitHub check run is |
| `llm_review` — the only LLM call in the system | The GitHub App private key |
| `explain` / audit persistence | Webhook verification |
| Subscription state | Rate limits against GitHub |

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
| `deployment.md` | Running it in a cluster, and the `talon-db` dependency chain — read before the first build |
| `testing.md` | Unit, ruleset, VCR, and the determinism test |
| `roadmap.md` | Plugin-scoped phases, and the phase-0 substrate this depends on |
| `OPEN-QUESTIONS.md` | What's still undecided |

## Decisions inherited from the ecosystem

The full list lives in [`talooner/README.md`](https://github.com/opentalon/talooner).
The ones that constrain this repo:

| # | Decision |
|---|---|
| 2 | **Thin stateless bot + `talooner-plugin` in an OpenTalon cluster.** Bot knows GitHub and nothing about Talon; plugin knows Talon and nothing about GitHub. |
| 3 | **Self-hosted. Forever.** The cluster holds the tenant's LLM credentials; the tenant pays for their own tokens. |
| 5 | **Facts live in `talon-db`**, per PR, persistent — required for reactive rules. |
| 7 | **Defeasible conflict resolution**, not ad-hoc "block wins". |
| 9 | **No LLM cache layer** — `llm_review` results are facts keyed by head sha, which is the cache. |
| 11 | **No dispatch actions.** The tenant's CI does the work and POSTs the result to the facts API; rules react. |
| 12 | **Two repos.** `talooner` (bot + CLI) and `talooner-plugin` (engine, fact store, proto). Separate concepts, separate versions. |

The shared proto lives here, since the plugin is the server and owns the
contract. The bot imports the generated Go package as a normal tagged dependency
— the same relationship `mcp-plugin` has with `opentalon`. Landing order for a
contract change: plugin first, tag, then bump the bot.

## First task

Recommended for whoever picks this up: write the example ruleset from
[`talooner/README.md`](https://github.com/opentalon/talooner) as a `.talon` +
`.talon.test` in `talon-language/examples/`, running on synthetic facts, no
GitHub involved. It answers most of the phase-0 table in `roadmap.md` and costs a
day. If it can't be written, the design is wrong and that's worth knowing before
any plugin code exists.

## Related repos

| Repo | Role |
|---|---|
| [`talooner`](https://github.com/opentalon/talooner) | The bot: GitHub App service + CLI. Consumes this plugin's contract |
| [`opentalon`](https://github.com/opentalon/opentalon) | Core orchestration platform and plugin host |
| [`talon-language`](https://github.com/opentalon/talon-language) | The Talon DSL: grammar, parser, inference engine, `.talon.test` |
| [`talon-db`](https://github.com/opentalon/talon-db) | Embedded fact store backing Talon |

## Contributing

Design phase, so the highest-value contribution right now is disagreement.
`OPEN-QUESTIONS.md` lists what's undecided; the §A items are answerable by
reading `talon-language` and each one blocks implementation.

## License

Apache-2.0. See `LICENSE`.
