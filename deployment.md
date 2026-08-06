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

## Version skew

Every tenant runs a different version, and there is no telemetry to tell anyone
which. Compatibility between `talooner` and `talooner-plugin` has to be an
explicit versioned contract, not an assumption — the version-skew failure mode the
workspace `CLAUDE.md` warns about, except here the two halves are operated by the
same person on the same box, which makes it tractable.

Landing order for a contract change: **plugin first, tag, then bump the bot.**
The proto lives here; the bot consumes the generated package as a tagged
dependency.

## What the tenant has to run

Not a service anyone signs up for. There is no hosted tier and no plan for one —
the cluster holds the LLM credentials, so every token a rule spends is billed to
whoever ran the rule.

1. A VPS running an **OpenTalon cluster**
2. `talooner-plugin` loaded in that cluster, with `talon-db` available
3. LLM provider credentials configured **in the cluster**
4. A GitHub App registered against their org, installed on their repos
5. The `talooner` bot running, holding the App private key and a cluster API key

Steps 4–5 are bot-side; see
[`talooner/auth.md`](https://github.com/opentalon/talooner/blob/main/auth.md).
