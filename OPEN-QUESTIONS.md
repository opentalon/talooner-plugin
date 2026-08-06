# `talooner-plugin` — open questions

Plugin-scoped. Ecosystem-wide questions (bot identity, GitHub App listing,
command semantics) live in
[`talooner/OPEN-QUESTIONS.md`](https://github.com/opentalon/talooner/blob/main/OPEN-QUESTIONS.md).

Resolved decisions are in `README.md`.

---

## A. Blocking phase 0 — verify against `talon-language`, don't assume

Answerable by reading code, not by discussion. Each one silently produces wrong
reviews if assumed. These are the same items as the phase-0 table in
`roadmap.md`, stated as questions.

**A1. Three-valued evaluation.** Does a condition on an unset fact evaluate to
unknown (rule doesn't fire), and is `not <unknown>` unknown rather than true?
Two-valued logic means a PR whose fact extraction failed gets auto-approved by
`not is "critical_path"`. Hard blocker. See `facts.md`, "Unset is not false".

**A2. List operands.** Do `contains` / `matches` (`grammar.ebnf:515`) quantify
existentially over a list like `pr.changed_files`, or are they string-only? Every
`pr.touches_*` predicate depends on it. If string-only: fix it generally in
`internal/executor`, or fall back to a joined string asserted by the bot?

**A3. Facts as action arguments.** Can an action take a fact reference rather
than a literal — `do assign "pr" "user.owner"`? The whole `user.*` namespace is
pointless if not.

**A4. Interpolation position.** Is `{ident.field}` (`grammar.ebnf:601`) available
in action arguments, or only in labels?

**A5. Cross-ruleset defeasible.** Does `overrides` / priority resolution work
across two rulesets loaded together (Talooner's `strict` base + the tenant's)?

**A6. External wake.** Can an out-of-band fact assertion wake the reactive engine
mid-PR? This is the *only* path for preview / screenshot / dependency-scan rules
now that dispatch is off the table — so it moved from nice-to-have to required.

**A7. `talon-db` at this shape.** Thousands of small, short-lived, concurrent
fact scopes — one per open PR — plus subscription state. Fits, or needs work?

**A8. Payload size.** Does a unary `Execute` with a fact blob plus ruleset text
plus a size-capped diff fit comfortably in `map<string, string>` args? Cap
default, and does `pr.diff_truncated` need to be a first-class fact? See
`protocol.md`.

---

## B. Still needing a call

**B1. Where does subscription state live?** It's cluster-side (the bot is
stateless), but is it a `talon-db` fact like everything else, or plugin-local
metadata outside the fact store? Fact is more consistent and makes
`when "pr.subscribed" == true` expressible; metadata is cleaner separation.
Leaning fact.

**B2. Retention defaults.** 30 days for facts, 1 year for decisions are
placeholders. Since the tenant runs their own storage, these could just as well
be "keep forever, you own the disk". Leaning: configurable, default 90d facts /
forever for decisions.

**B3. Does a repeat `evaluate_pr` at an unchanged sha re-run `llm_review`?** The
fact cache says no by construction, but `@talooner /review --force` implies a
bypass. If a force path exists, it has to be an explicit arg on `evaluate_pr` and
it has to respect the per-tenant budget ceiling. Leaning: `force` arg, cache
bypass, cap still applies.

**B4. What does `mode: plan` return?** Same shape with a flag, or a distinct
`plan[]` field the bot can't accidentally execute? The second is harder to misuse
— an action list that reaches the GitHub executor by accident is a real write to
someone's repo. Leaning distinct field.

---

## C. Deferred to the phase that needs them

- Org-level shared rulesets and non-overridable org policy (phase 4)
- Community ruleset distribution and versioning (phase 4)
- `k8s-operator` first-class support in the CRD (phase 4)
- A second `llm_review` variant taking `module.documentation_urls` as a list,
  should single-module evaluation prove too coarse (`facts.md`, "Cardinality")

---

## Licensing

**Apache-2.0**, matching `talooner` and the rest of the workspace. Settled; the
reasoning is in
[`talooner/OPEN-QUESTIONS.md`](https://github.com/opentalon/talooner/blob/main/OPEN-QUESTIONS.md).

The one alternative that was considered and rejected for this repo specifically:
BSL 1.1 on the plugin only, to block someone building the hosted service the
project declined to build. Rejected because fencing the plugin is the worst
version of open-core here — the plugin is where the OpenTalon dogfooding happens,
so closing it means the interesting half is the part nobody can read, learn from,
or contribute to.
