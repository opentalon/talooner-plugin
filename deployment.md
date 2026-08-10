# `talooner-plugin` — deployment

## Dependency chain — read before the first build

`talooner-plugin` links `talon-language`, which carries
`replace github.com/opentalon/talon-db => ../talon-db`. **A `replace` is not
transitive** — the consuming module must restate it. So `talooner-plugin/go.mod`
needs its own `replace` line:

```
replace github.com/opentalon/talon-db => ../talon-db
```

*and* a sibling `talon-db/` checkout, plus the CI clone step that
`talon-language/.github/workflows/ci.yml` already uses.

Without it every build fails with:

```
github.com/opentalon/talon-db@v0.0.0-...: replacement directory ../talon-db does not exist
```

This is the documented workspace convention, not a bug to fix. Recover with:

```bash
git clone https://github.com/opentalon/talon-db.git <workspace>/talon-db
```

Directory layout therefore matters: `talooner-plugin/`, `talon-language/` and
`talon-db/` must be siblings.

## In a cluster

The plugin is a normal OpenTalon plugin, so the existing `PluginConfig` in
`k8s-operator`'s CRD already fits — no operator changes needed to *run* it
(`k8s-operator/api/v1alpha1/opentaloninstance_types.go:312`):

```yaml
spec:
  config:
    plugins:
      - name: talooner
        source: github.com/opentalon/talooner-plugin@v0.1.0
        env:
          - name: TALOONER_DB_PATH
            value: /data/talooner.db
    models:
      - name: reviewer
        provider: anthropic
        apiKeySecret:
          name: talooner-llm
          key: api-key
```

The phase-4 `k8s-operator` item in `roadmap.md` is about first-class ergonomics
(a `talooner:` block, sane defaults, PVC sizing), not about feasibility.

## Exposing the cluster

New requirement under decision 1, and the main thing this repo inherited from it.
The caller used to be a process on the same box. It is now a GitHub Actions
runner, so **something has to be reachable from outside the tenant's network** —
or the runner has to be brought inside it.

| Option | Setup | Trade-off |
|---|---|---|
| **Public gRPC + TLS + API key** (default) | Terminate TLS at the cluster or a proxy; tenant sets `OPENTALON_HOST` | Simplest, works with GitHub-hosted runners. The endpoint is on the internet and the API key is the whole gate |
| Self-hosted runner | Runner inside the network, cluster stays private | No exposure at all; the tenant now operates runners, which is real ops |
| VPN join as a workflow step | Tailscale/WireGuard step before the action | Middle ground, one more moving part in every run, and a second credential in the workflow |

IP allowlisting is not a fourth option worth much: GitHub's hosted runner ranges
are large, change, and cover every other GitHub customer's runners too. Allowing
them is barely narrower than allowing the internet. It is a defence-in-depth
nicety, not a gate — the API key is the gate.

What the cluster should do about being exposed, since it now is:

- **Rate limit per API key.** A leaked key's first symptom is spend; the ceiling
  in `llm-review.md` caps the damage, and a request-rate limit caps the noise.
- **Fail closed on auth.** No anonymous `whoami`, no unauthenticated health
  endpoint that leaks tenant names or model ids.
- **Log the caller.** `repo`, `pr`, and the workflow run id, so a tenant can
  answer "which repo burned my quota" without a model in the loop.

## Version skew

Every tenant runs a different version, and there is no telemetry to tell anyone
which. Compatibility between `talooner` and `talooner-plugin` has to be an
explicit versioned contract, not an assumption — the version-skew failure mode
the workspace `CLAUDE.md` warns about.

Decision 1 made this **worse**, and it's worth being blunt about. Previously both
halves were operated by one person on one box, so skew was tractable. Now:

- the action version is pinned in each repo's `.github/workflows/talooner.yml`,
  possibly by someone who doesn't run the cluster,
- the plugin version is whatever the cluster operator deployed,
- a tenant with 30 repos has 30 independently pinned callers,
- and neither side gets told when the other upgrades.

Hence `protocol_version` on `whoami`: the plugin rejects callers below its
protocol floor rather than guessing (`protocol.md`, and `OPEN-QUESTIONS.md` B5).
A run that can't be served must fail with one clear message at the top, not
misbehave subtly halfway through an evaluation.

Landing order for a contract change is unchanged: **plugin first, tag, then bump
the action.** The proto lives here; the action consumes the generated package as
a tagged dependency. But "bump the action" now means tenants editing workflow
files, so the contract has to stay backward-compatible across at least one major
version — you cannot assume callers upgrade.

## What the tenant has to run

Not a service anyone signs up for. There is no hosted tier and no plan for one —
the cluster holds the LLM credentials, so every token a rule spends is billed to
whoever ran the rule.

1. A VPS running an **OpenTalon cluster**, reachable from wherever their runners
   are
2. `talooner-plugin` loaded in that cluster, with `talon-db` available
3. LLM provider credentials configured **in the cluster**
4. `.github/workflows/talooner.yml` in each reviewed repo
5. `OPENTALON_HOST` + `OPENTALON_API_KEY` as repo or org secrets

Steps 4–5 are caller-side and take minutes; see
[`talooner/auth.md`](https://github.com/opentalon/talooner/blob/main/auth.md).
There is no App to register and no bot process to keep running — the entire
standing cost is items 1–3, which is this repo.
