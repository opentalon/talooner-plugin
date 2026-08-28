# `talooner-plugin` — roadmap

Plugin-scoped slice of the ecosystem roadmap. The bot's half and the phase exit
criteria that involve GitHub live in
[`talooner/roadmap.md`](https://github.com/opentalon/talooner/blob/main/roadmap.md);
phases are numbered the same in both so they can be read side by side.

Phases are cumulative. Each has an exit criterion that is a thing you can run,
not a checkbox.

---

## Phase 0 — verify the substrate ✅ answered, substrate fixed

**No Talooner code.** Answer whether `tln-language` and `tln-db` can express
what this design assumes, and fix them where they can't. Every item silently
produces wrong reviews if assumed rather than checked.

Verified 2026-08-06 by reading the code and running probes against both backends.
The three substrate fixes it filed landed 2026-08-07. Findings and the decisions
they forced are in `OPEN-QUESTIONS.md`; the summary:

| Item | Result |
|---|---|
| Three-valued evaluation; `not <unknown>` is unknown | ❌ **No.** Two-valued, closed-world negation-as-failure — a PR with `critical_path` unset matches `not is "critical_path"` and is allowed. Accepted for v1 as a known risk (A1); `facts.md`, "Unset is false" |
| `contains`/`matches` quantify existentially over list operands | ❌ **Not then, ✅ now.** Was string-only with a silent false on a list; fixed upstream — [`tln-language#158`](https://github.com/opentalon/tln-language/issues/158), landed 2026-08-07 in `tln-language` 35109f0 + `tln-db` e1c8ddb. `pr.touches_*` unblocked. Note `matches` is substring, not glob (A2) |
| Facts usable as action arguments | ✅ **Yes**, fully — `attr "x"` passes typed values (lists included), plus arithmetic and string builtins |
| A `do <verb> <args>` action clause on rules | ❌ **Not then, ✅ now.** The grammar had no `do` verb at all — actions were only `mcp "server" "tool" { key Expr }`, so the whole 7-verb vocabulary was unwritable. Added upstream 2026-08-07 ([`tln-language/docs/actions.md`](https://github.com/opentalon/tln-language/blob/main/docs/actions.md)) along with `did` / `did_not` test assertions and list literals in `given` |
| `{ident.field}` interpolation in action args | ✅ **Yes** in action args. But `{item.<field>}` resolves only for `item.name`; `{item.id}` renders literally. Use `{id}` / `{attr.x}` |
| Cross-ruleset defeasible resolution (base + tenant loaded together) | ✅ **Yes** when both are one compiled program; `overrides` across separately-compiled programs is still a compile error. `import` used to let a caller shadow a `strict` rule by name — [`tln-language#159`](https://github.com/opentalon/tln-language/issues/159), landed 2026-08-07 in d509092, now a hard error. The concatenation interim is dropped; load via `import` (A5) |
| ~~External fact assertion wakes reactive rules~~ | **Dropped from phase 0** by decision 20 — nothing is alive to act on a wake, so `assert_facts` is a store-only write in v1 and the fact is read at the next `evaluate_pr`. Returns in phase 4 with dispatch-driven wake |
| `tln-db` handles many small, short-lived, concurrent scopes | ⚠️ **Fits the data, not the API.** No drop-scope, append-only id map, single-writer bbolt, and a non-atomic read-modify-write `Assert`. One doc per PR keyed `{repo}#{number}`; overlapping runs rejected with 409 (A7, B6) |
| Plugin protocol fits a large fact payload | ✅ **32 MiB, on purpose.** Was ~4 MiB by accident — grpc-go's default, no options set anywhere in `opentalon`. Fixed in core — [`opentalon#325`](https://github.com/opentalon/opentalon/issues/325), landed 2026-08-07 in 4cbc14d; `OPENTALON_GRPC_MAX_MSG_BYTES` overrides. Bot-side 1 MiB diff cap stays either way (A8) |

**Exit:** a `.tln` file in `tln-language/examples/` expressing the brief's
ruleset, with a `.tln.test` that passes, running against synthetic PR facts. No
GitHub involved.

**Met, 2026-08-07.** The artifact is
[`examples/talooner_review.tln`](https://github.com/opentalon/tln-language/blob/main/examples/talooner_review.tln)
plus its `.tln.test` — the brief's v1 ruleset on synthetic PR facts, 14 tests
passing, no GitHub involved.

Writing it found one thing verification had missed, because verification checked
conditions and never checked actions: **the grammar had no `do` verb.** Every
ruleset in these docs was written in a syntax the parser rejected. Fixed
upstream rather than worked around, along with the two test-DSL gaps that made
the artifact unwritable — no list literals in `given`, no way to assert an
action fired. Phase 1 can start.

The fixes landed in `tln-language` / `tln-db` / `opentalon` as their own PRs,
per the workspace's one-repo-at-a-time rule.

---

## Phase 1 — the walking skeleton

Loads in a cluster and answers `evaluate_pr`. No LLM.

- Loads as an OpenTalon plugin, `tln-db` attached
- Owns the proto; the bot consumes the generated package as a tagged dep
- Actions: `evaluate_pr`, `is_subscribed`, `set_subscription`, `validate_ruleset`,
  `whoami`. All `user_only: true`
- `whoami` returns `protocol_version`, and the plugin rejects callers below its
  floor with one clear error — the action is versioned per tenant repo now
  (`deployment.md`, "Version skew")
- Per-PR fact scoping, full re-derivation with retraction
- Subscription state, retention
- Reachable from a GitHub-hosted runner: TLS, auth on every action, per-key rate
  limit (`deployment.md`, "Exposing the cluster"). Phase 1 is the first time
  this thing has a client it doesn't run
- Returned action vocabulary: `comment` and whatever the check run needs

**Exit:** a workflow run posts a failing check run and one comment for a PR with
no description, and re-evaluating after a push flips it green — driven entirely
by actions this plugin returned, to a caller that exits between the two.

---

## Phase 2 — the full deterministic vocabulary

Everything that doesn't need a model.

- Full action vocabulary: `approve`, `block`, `assign`, `require`, `emit`,
  `notify`
- Retraction semantics for each (`engine.md`)
- Defeasible conflict resolution + the Talooner `strict` base ruleset
- `mode: plan` — evaluate and return a distinct `plan[]` field, never an
  `actions` key (`OPEN-QUESTIONS.md` B4), for head-branch rulesets on fork PRs
- `assert_facts` with namespace enforcement — the custom-facts path. Store-only:
  accepts, validates, persists, returns no actions (decision 20)
- `explain_pr`
- `user.*` and `module.*` resolution as action arguments

**Exit:** the brief's ruleset minus the `llm_review` rules runs against a real
repo, and the `opentalon/*` repos dogfood it.

---

## Phase 3 — `llm_review`

The only place a model enters, and it enters as a fact. Details in
`llm-review.md`.

- LLM call routed **through the OpenTalon host** (`_subprocess.run` over the
  plugin callback channel), using the cluster's credentials — the plugin embeds
  no provider SDK. Requires running under a host; standalone TCP mode withdraws
  the feature and degrades a fired `llm_review` to `result: "error"`
- Prompt in a `.txt` file, never a Go literal
- Fixed output enum, result stored as a fact keyed by
  `(pr, head_sha, doc_url, prompt_version)`
- Per-PR conversation retained, each review a scoped turn
- Per-PR call cap, per-tenant budget ceiling, quota surfaced via `whoami`
- `force` arg on `evaluate_pr` — cache bypass for `@talooner /review --force`,
  with the cap and ceiling still applying (`protocol.md`)
- Per-module evaluation cardinality decided and implemented (`facts.md`)
- VCR cassettes

**Exit:** a PR whose code contradicts its module docs gets blocked with a
specific, quotable explanation — and re-running at the same sha makes no second
API call and produces byte-identical output.

---

## Phase 4 — ecosystem

- **Ruleset sharing.** Community rulesets, versioned and importable
  (`tln-language/internal/imports` already exists)
- **Org-level rulesets.** One ruleset many repos import, optionally
  non-overridable by the repo
- **Reactive wake**, if manual `/review` after an externally POSTed fact proves
  annoying (decision 20). This is where phase-0 item A6 comes back: dispatch
  starts a run that is alive to act, so `assert_facts` can return actions again
- `k8s-operator` first-class support so "run a cluster" is a manifest

---

## What this drags into OpenTalon

Half the work, and it lands in other repos:

| Repo | Likely work |
|---|---|
| `tln-language` | **Landed 2026-08-07:** list-operand quantification ([#158](https://github.com/opentalon/tln-language/issues/158), 35109f0 — was the gate on phase 0's exit), import shadowing of `strict` rules ([#159](https://github.com/opentalon/tln-language/issues/159), d509092), the `do` action clause + `did`/`did_not` test assertions + list literals in `given`. **Verified fine:** facts as action arguments, interpolation in action args, defeasible resolution across a combined ruleset. **Declined for v1:** three-valued evaluation — the risk is accepted instead (A1). **Still open, not blocking:** glob path matching — `matches` is substring locally and term-AND on Datalevin, so path predicates are written with `contains`/`ends_with`. *External fact assertion waking reactive rules is deferred to phase 4 — decision 20* |
| `tln-db` | Bulk delete / drop-scope for retention; atomic or CAS `Assert` (worked around by B6's 409 for now); many-small-scopes performance under real load |
| `opentalon` | **Landed 2026-08-07:** configurable gRPC message-size limits, 32 MiB default ([#325](https://github.com/opentalon/opentalon/issues/325), 4cbc14d). **Still ours to drive:** tenant credential storage + quota accounting; `whoami` capability handshake **plus a protocol version**; a gRPC surface safe to expose publicly — TLS, auth on every action, per-key rate limiting — now that the caller is a GitHub runner rather than a process on the same box |
| `k8s-operator` | `talooner-plugin` in the instance CRD |

Order matters and the workspace rule applies: land core changes first, then bump
dependents. A change spanning `tln-language` and this repo is two PRs in two
repos.

---

## Deliberately out of scope

- **An LLM anywhere in the decision path.** It answers questions; rules decide.
- **Any knowledge of GitHub.** If this repo ever needs a GitHub fixture to test
  something, the seam has been broken.
- **Dispatch verbs** (`deploy_preview`, `screenshot`, `scan_dependencies`). The
  facts API covers it.
