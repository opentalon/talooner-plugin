# `talooner-plugin` — open questions

Plugin-scoped. Ecosystem-wide questions live in
[`talooner/OPEN-QUESTIONS.md`](https://github.com/opentalon/talooner/blob/main/OPEN-QUESTIONS.md).
Resolved decisions are in `README.md`.

**Nothing here is open, and nothing is blocked.** Phase-0 substrate verification
is done — read against `tln-language` and `tln-db` at 2026-08-06, with
runnable probes — and every question that followed from it has a call. The record
below is kept because several of the answers are non-obvious and one (A1) is an
accepted risk whose reasoning should survive the next person who finds it.

All three substrate fixes landed 2026-08-07. Nothing gates the design any more:

| Issue | Was blocking | Landed as |
|---|---|---|
| [`tln-language#158`](https://github.com/opentalon/tln-language/issues/158) | every `pr.touches_*` predicate | `tln-language` 35109f0 (PR #160) + `tln-db` e1c8ddb — string predicates and full text quantify over lists on both backends |
| [`tln-language#159`](https://github.com/opentalon/tln-language/issues/159) | `import`-based ruleset loading | `tln-language` d509092 — redefining an imported name is a compile error |
| [`opentalon#325`](https://github.com/opentalon/opentalon/issues/325) | payload headroom | `opentalon` 4cbc14d — 32 MiB default, `OPENTALON_GRPC_MAX_MSG_BYTES` to override |

The bot-side diff cap (A8) stays regardless; it was never only a workaround.

---

## Resolved

**A1. Unset facts are false — accepted for v1, with caveats.**

The evaluator is two-valued with closed-world negation-as-failure: a missing
attribute makes its pattern fail, which makes the enclosing `not` succeed
(`internal/factstore/memory.go:691,773`; `tln-db/bboltstore/query.go:314` —
both backends agree, and there is no `unknown` anywhere in the evaluator).
Probed directly:

```tln
define "critical_path" { attr "critical_path" == true }

rule "auto approve" {
  for records where type == "pr" and not is "critical_path"
  allow "merge"
}
```

A PR with `critical_path` **unset** matched and was allowed. `is` is plain
inlining of the define's conditions (`internal/planner/planner.go:1983`), so it
carries no separate truth value.

**Decision: approve on missing facts, don't block.** Caveats, discussed and
knowingly accepted:

- The failure is silent and inverted. Extraction throwing on a malformed diff
  produces an *approval* with a review comment stating no problems were found —
  because from the engine's view, nothing was. There is no signal distinguishing
  "checked, clean" from "never checked".
- It applies to every predicate, not just `critical_path`. Any rule of the form
  `not is "X"` or `not attr "X" == true` reads a failed extraction as a pass.
- The blast radius scales with the ruleset. Each new `not`-shaped rule adds
  another path from "extractor crashed" to "approved", and none of them are
  visible in review output.

**The guard we chose not to build**, kept here so the option isn't rediscovered
from scratch: negation-as-failure works *in our favour* for a completeness flag.
Extractors would assert `pr.facts_complete` last, and

```tln
strict rule "incomplete facts" {
  for records where type == "pr" and not attr "facts_complete" == true
  block "merge"
  reason "fact extraction incomplete — not evaluated"
}
```

fires exactly when extraction died, because the missing flag makes the `not`
true. `strict` means a tenant ruleset can't override it — overriding a strict
rule is a compile error (`internal/validator/validator.go:316`). Cost is one rule
plus the discipline that extractors set the flag only on full success. No engine
change, no phase-0 delay.

Nothing in v1 forecloses adding it later. Revisit if this bites — the symptom to
watch for is an approval on a PR that a human then finds obvious problems in.

**A2. List operands — fixed upstream, landed.**
[`tln-language#158`](https://github.com/opentalon/tln-language/issues/158),
shipped in `tln-language` 35109f0 and `tln-db` e1c8ddb. `contains` /
`starts_with` / `ends_with` now quantify existentially over `[]string` / `[]any`
subjects — the condition holds when any element satisfies it
(`internal/factstore/memory.go:830`). Same for full text (`matches` /
`matches_phrase`), and the `tln-db` evaluator agrees, so the store and language
layers no longer disagree. `==` stays strict equality against the whole list.

Two edges the `pr.touches_*` predicates have to respect:

- **A list with no string elements matches nothing** — it does not fall back to
  the scalar path. An empty `pr.changed_files` matches no predicate, which is the
  wanted direction.
- **`matches` is not a glob.** Locally it is a contiguous case-insensitive
  substring scan; on Datalevin it is term-AND. `matches "**/*.css"` matches
  nothing, because no path literally contains `**/*.css`. Path predicates use
  `contains` / `ends_with`. Fixed in `facts.md`, which had glob-shaped examples.

**A5. Ruleset loading — fixed upstream, landed. Use `import`.**
[`tln-language#159`](https://github.com/opentalon/tln-language/issues/159),
shipped in `tln-language` d509092. A caller block that redefines an imported
name is now a hard error naming the imported file and line
(`internal/imports/resolve.go:207`), so a tenant can no longer delete a `strict`
base rule by naming a rule identically. Defeasible resolution across a combined
base + tenant ruleset already worked (`internal/defeasible/defeasible.go:33`).

**The interim is dropped:** the base ruleset is `import`ed, not concatenated with
the tenant's into one source. Both give the same protection now, and `import`
keeps the base ruleset a file the tenant can read and the plugin can version
rather than a string join. The tenant-visible failure is a compile error at
`validate_ruleset` time telling them to rename.

**A7. Storage shape — one doc per PR, keyed `{repo}#{number}`.** `tln-db` is
keyed `(entity_id, doc_id)` with `entity_id` pinned to one tenant per client
(`internal/tlndb/adapter.go:101`), so a PR is a document, not its own scope.
Follows from that:

- Scoping a run to one PR means injecting the `pr_key` pattern into every
  selector at load time — invisible to tenant rule authors, but it has to happen
  or every rule sees every PR's records.
- Retention is a sweeper (`Scan` + per-doc `Delete`), not a config key. There is
  no bulk delete; `idmap` entries survive deletion permanently
  (`tln-db/bboltstore/idmap.go:14`), so the bbolt file never shrinks.
- `Assert` is a non-atomic read-modify-write with no CAS
  (`internal/tlndb/adapter.go:103-123`). Overlapping writers interleave into a
  mixed document rather than last-write-wins. Closed by B6.

**A8. Payload ceiling — fixed in core, landed.**
[`opentalon#325`](https://github.com/opentalon/opentalon/issues/325), shipped in
`opentalon` 4cbc14d. Previously no message-size options were set on any
host↔plugin path, so gRPC's 4 MiB default receive limit governed every call and
the failure past it was a transport error naming no field — bad, because one
`evaluate_pr` carries the fact blob, the ruleset text, and the diff inline in a
single `map<string, string>` (`opentalon/proto/plugin.proto:93`), and the ceiling
applies to their sum. Core now sets limits symmetrically on server and client
(`opentalon/internal/grpclimit`): **32 MiB default, `OPENTALON_GRPC_MAX_MSG_BYTES`
to override**, applied to both plugin and channel paths.

Consequences for us:

- Headroom is no longer the constraint. A 1 MiB diff against 32 MiB is
  comfortable, so the cap below is a policy choice about LLM cost and review
  quality, not a transport workaround.
- The override is an *operator* knob on the cluster, not something the action
  sets. A tenant who raises it has to raise it on both ends; the plugin should
  not assume more than the default.

Two things stay ours:

- **A bot-side diff cap.** Even with the larger ceiling, the bot caps before
  sending so the failure is our error message rather than a transport error.
  1 MiB diff against the 32 MiB transport default.
- **`pr.diff_truncated` as a first-class fact.** Under the A1 decision an unset
  fact reads as false, so without it "the diff had no problems" and "we never saw
  the diff" are the same value to the engine — the exact A1 failure mode, on the
  one input most likely to hit a size limit.

**B1. Subscription state lives in the fact store.** A `tln-db` fact like
everything else, so `when attr "pr.subscribed" == true` is expressible and there's one
storage story rather than two.

**B2. Retention defaults stand** — 90d facts, forever for decisions,
configurable. Per A7 this is a GC job phase 2 owes, not a config value.

**B4. `mode: plan` returns a distinct `plan[]` field**, not the normal shape with
a flag:

```jsonc
{ "plan": [ {"kind": "request_changes", ...} ] }   // no "actions" key at all
```

The alternative — `{"actions": [...], "planned": true}` — puts dry-run output in
the same field the GitHub executor reads, so correctness depends on every caller
path checking `planned` first. A retry wrapper, an error path, a later refactor,
or a third-party consumer that forwards `actions` without the check performs real
writes on someone's repo during what the caller believed was a dry run. With a
distinct field that's structurally impossible: the data never occupies the field
that triggers a write. Costs one branch at the call site.

**B5. `whoami` carries a protocol version and the plugin rejects callers it
can't serve.** The action version is pinned per repo in a workflow file
(`opentalon/talooner@v1`, or a sha), the plugin version is whatever the cluster
runs, and neither party sees the other's upgrade. The plugin refuses calls below
its floor rather than guessing; the action fails the run with a clear message.

**B6. Overlapping runs are rejected, not queued or locked.** A second
`evaluate_pr` for a `(repo, pr)` already in flight gets a 409 Conflict. This also
closes the A7 read-modify-write race — no interleaved writers, so no mixed
document — without a lock or upstream batching. `concurrency:` in the tenant's
workflow file remains the first line of defence; the 409 is what happens when
they delete it.

**B7. `run_ruleset_test` — designed, blocked on an upstream export.** `talooner`
issue #24 (`rules test`) needs a cluster action that runs a tenant's
`rules.tln.test` file, the way `validate_ruleset` already runs `rules.tln`
through the same compiler the CLI would otherwise duplicate. It cannot be built
yet: `talooner-plugin` is a separate Go module from `tln-language`, and
`internal/testrunner` is walled off by Go's internal-package visibility, which
is scoped by import path prefix, not by module boundary — this module can never
import it directly, regardless of what `testing.md`'s "reuse `internal/testrunner`
directly" line implies. `cmd/tln test` only gets away with it because it lives
inside the `tln-language` module (`cmd/tln/main.go:344`, `runTestPair`): lex+parse
the rules file and the test file separately, merge the two ASTs (`did`/`did_not`
assertions need the rule blocks the test file alone doesn't carry), compile,
`testrunner.Run(merged, plans)`. `pkg/tln` — the public surface `ruleset.go`
already builds on for `Check`/`Run` — has no equivalent today.

Filed upstream: [`tln-language#200`](https://github.com/opentalon/tln-language/issues/200),
proposing `pkg/tln.RunTests(rulesSource, testSource string, opts ...Option)
([]TestResult, error)`, mirroring `Check`/`Run`'s shape (`*CompileError` on
failure, one `TestResult{Name, Passed, Errors, Duration}` per test block on
success — the same fields `internal/testrunner.TestResult` already has, exported
at the boundary this module can actually cross).

Once that lands, the shape here is small:

- Proto: `RunRulesetTestRequest{ruleset, test_source}` /
  `RunRulesetTestResponse{results: repeated TestOutcome{name, passed, errors},
  diagnostics}` — `diagnostics` reuses the same `Diagnostic` message and
  `toProtoDiagnostics` conversion `validate_ruleset` already has, so a compile
  failure looks identical whether it's hit via `rules validate` or `rules test`.
- `internal/service/test.go` (new file, mirrors `validate.go`): compile
  tenant+base the way `ruleset.Load` does, then call `pkg/tln.RunTests`.
- New action `run_ruleset_test`, registered `user_only` like every other action.
- `talooner rules test <path>` rounds-trips to it the same way `rules validate`
  already does to `validate_ruleset` — no second compiler client-side.
