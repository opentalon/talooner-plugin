# `talooner-plugin` — roadmap

Plugin-scoped slice of the ecosystem roadmap. The bot's half and the phase exit
criteria that involve GitHub live in
[`talooner/roadmap.md`](https://github.com/opentalon/talooner/blob/main/roadmap.md);
phases are numbered the same in both so they can be read side by side.

Phases are cumulative. Each has an exit criterion that is a thing you can run,
not a checkbox.

---

## Phase 0 — verify the substrate

**No Talooner code.** Answer whether `talon-language` and `talon-db` can express
what this design assumes, and fix them where they can't. Every item silently
produces wrong reviews if assumed rather than checked.

| Item | Where | Consequence if it doesn't hold |
|---|---|---|
| Three-valued evaluation; `not <unknown>` is unknown | `talon-language/internal/executor` | Failed extraction auto-approves critical PRs. **Hard blocker.** See `facts.md`, "Unset is not false" |
| `contains`/`matches` quantify existentially over list operands | `internal/executor`, `grammar.ebnf:515` | Every `pr.touches_*` predicate is unimplementable as designed |
| Facts usable as action arguments (`do assign "pr" "user.owner"`) | `internal/executor` | The whole `user.*` namespace is dead weight |
| `{ident.field}` interpolation in action args, not only labels | `grammar.ebnf:601` | Comment templating doesn't work |
| Cross-ruleset defeasible resolution (base + tenant loaded together) | `internal/defeasible` | The `strict` base ruleset can't protect anything |
| External fact assertion wakes reactive rules | `internal/reactive` | `assert_facts` does nothing; preview/screenshot/scan rules never fire |
| `talon-db` handles many small, short-lived, concurrent scopes | `talon-db` | Doesn't scale past a handful of open PRs |
| Plugin protocol fits a large fact payload | `opentalon/pkg/plugin` | The bot↔plugin seam; see `protocol.md`, "Open: payload size" |

**Exit:** a `.tln` file in `talon-language/examples/` expressing the brief's
ruleset, with a `.tln.test` that passes, running against synthetic PR facts. No
GitHub involved. If this can't be written, the design is wrong and it's cheap to
find out now.

Fixes land in `talon-language` / `talon-db` as their own PRs, per the workspace's
one-repo-at-a-time rule.

---

## Phase 1 — the walking skeleton

Loads in a cluster and answers `evaluate_pr`. No LLM.

- Loads as an OpenTalon plugin, `talon-db` attached
- Owns the proto; the bot consumes the generated package as a tagged dep
- Actions: `evaluate_pr`, `is_subscribed`, `set_subscription`, `validate_ruleset`,
  `whoami`. All `user_only: true`
- Per-PR fact scoping, full re-derivation with retraction
- Subscription state, retention
- Returned action vocabulary: `comment` and whatever the check run needs

**Exit:** the bot posts a failing check run and one comment for a PR with no
description, and re-evaluating after a push flips it green — driven entirely by
actions this plugin returned.

---

## Phase 2 — the full deterministic vocabulary

Everything that doesn't need a model.

- Full action vocabulary: `approve`, `block`, `assign`, `require`, `emit`,
  `notify`
- Retraction semantics for each (`engine.md`)
- Defeasible conflict resolution + the Talooner `strict` base ruleset
- `mode: plan` — evaluate without returning executable actions, for head-branch
  rulesets on fork PRs
- `assert_facts` with namespace enforcement — the custom-facts path
- `explain_pr`
- `user.*` and `module.*` resolution as action arguments

**Exit:** the brief's ruleset minus the `llm_review` rules runs against a real
repo, and the `opentalon/*` repos dogfood it.

---

## Phase 3 — `llm_review`

The only place a model enters, and it enters as a fact. Details in
`llm-review.md`.

- LLM call using cluster-configured tenant credentials
- Prompt in a `.txt` file, never a Go literal
- Fixed output enum, result stored as a fact keyed by
  `(pr, head_sha, doc_url, prompt_version)`
- Per-PR conversation retained, each review a scoped turn
- Per-PR call cap, per-tenant budget ceiling, quota surfaced via `whoami`
- Per-module evaluation cardinality decided and implemented (`facts.md`)
- VCR cassettes

**Exit:** a PR whose code contradicts its module docs gets blocked with a
specific, quotable explanation — and re-running at the same sha makes no second
API call and produces byte-identical output.

---

## Phase 4 — ecosystem

- **Ruleset sharing.** Community rulesets, versioned and importable
  (`talon-language/internal/imports` already exists)
- **Org-level rulesets.** One ruleset many repos import, optionally
  non-overridable by the repo
- `k8s-operator` first-class support so "run a cluster" is a manifest

---

## What this drags into OpenTalon

Half the work, and it lands in other repos:

| Repo | Likely work |
|---|---|
| `talon-language` | Three-valued evaluation guarantees; list-operand string operators; interpolation in action args; cross-ruleset defeasible resolution; external fact assertion waking reactive rules |
| `talon-db` | Many-small-scopes performance; retention/TTL; audit-oriented queries |
| `opentalon` | Plugin protocol fit for large payloads; tenant credential storage + quota accounting; `whoami` capability handshake |
| `k8s-operator` | `talooner-plugin` in the instance CRD |

Order matters and the workspace rule applies: land core changes first, then bump
dependents. A change spanning `talon-language` and this repo is two PRs in two
repos.

---

## Deliberately out of scope

- **An LLM anywhere in the decision path.** It answers questions; rules decide.
- **Any knowledge of GitHub.** If this repo ever needs a GitHub fixture to test
  something, the seam has been broken.
- **Dispatch verbs** (`deploy_preview`, `screenshot`, `scan_dependencies`). The
  facts API covers it.
