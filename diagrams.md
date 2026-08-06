# `talooner-plugin` — design diagrams

Mermaid, so it renders on GitHub and stays diffable in review.

The full set — system context, the bot's internals, PR lifecycle, credential
blast radius — lives in
[`talooner/diagrams.md`](https://github.com/opentalon/talooner/blob/main/diagrams.md).
This file carries the ones this component is in.

**C4 for structure, UML for behaviour.** Nothing else. Not Mermaid's native
`C4Context` / `C4Container` syntax, despite following the C4 model — it's still
experimental and has no real layout engine, so relationship labels land on top of
the boxes. Plain `flowchart` renders the same model correctly.

| # | Diagram | Answers |
|---|---|---|
| 1 | Containers (C4 L2) | Where this plugin sits, and what it may talk to |
| 2 | Components (C4 L3) | Internals, in execution order |
| 3 | `evaluate_pr` | The request this component exists to serve |
| 4 | Re-evaluation and retraction | Why facts are re-derived, never deltaed |
| 5 | `llm_review` | Why there's no cache layer, and where determinism comes from |
| 6 | Fact sources | Where every fact comes from, and which ones CI may write |

All diagrams verified rendering with `mermaid-cli` 11.16.

---

## 1. Containers — C4 L2

Everything inside the dashed boundary runs on the tenant's VPS. There is no
hosted Talooner, no shared service, no third party.

```mermaid
flowchart TB
    subgraph GH["GitHub — SaaS"]
        direction LR
        APP["App installation<br/>webhooks out"]
        GAPI["REST / GraphQL API"]
    end

    CI["<b>Tenant CI</b><br/>preview builds · scans<br/>screenshots"]

    subgraph VPS["Tenant VPS"]
        direction TB
        BOT["<b>talooner</b> — the bot<br/>GitHub App service + CLI<br/><i>stateless</i>"]

        subgraph CLUSTER["OpenTalon cluster"]
            direction LR
            PLUGIN["<b>talooner-plugin</b><br/>Talon engine · rulesets<br/>llm_review · explain"]
            DB[("<b>talon-db</b><br/>facts · decisions<br/>subscriptions")]
            PLUGIN <--> DB
        end

        BOT <-->|"gRPC — actions"| PLUGIN
    end

    LLM["<b>LLM provider</b><br/>tenant account, tenant budget"]

    APP -->|"issue_comment<br/>pull_request<br/>check_suite"| BOT
    CI -->|"POST /api/v1/facts"| BOT
    BOT -->|"installation token<br/>reviews · comments<br/>check runs"| GAPI
    PLUGIN -->|"tenant credentials<br/>live only here"| LLM

    classDef ext fill:#f4f4f4,stroke:#8a8a8a,stroke-dasharray:5 4,color:#333
    classDef own fill:#e8f0fe,stroke:#3b6bb5,color:#102040
    classDef store fill:#ede7f6,stroke:#6a4fa3,color:#221040
    class APP,GAPI,LLM,CI ext
    class BOT own
    class PLUGIN own
    class DB store
    style VPS fill:#fbfdff,stroke:#3b6bb5,stroke-width:2px,stroke-dasharray:7 5
    style CLUSTER fill:#f2f8f0,stroke:#4a8f3c
    style GH fill:#fafafa,stroke:#8a8a8a,stroke-dasharray:5 4
```

Two things to read off this diagram:

- **This plugin has no arrow to GitHub.** Not "shouldn't" — doesn't. Every GitHub
  edge belongs to the bot. If one ever appears here, the seam is broken and the
  testing story goes with it.
- **The bot never touches an LLM.** Provider credentials live in the cluster and
  nowhere else. Compromise the bot, you get GitHub comment access; compromise the
  cluster, you get LLM spend. Never both.

---

## 2. Components — C4 L3

Knows Talon, knows nothing about GitHub. Prose in `engine.md`.

```mermaid
flowchart TB
    IN(["action evaluate_pr<br/>facts JSON + ruleset text"]) --> RPC
    RPC["gRPC surface<br/>owns the proto"] --> RULES
    RULES["Ruleset loader<br/>parse · validate · compile"] --> ENGINE
    BASE["Talooner base ruleset<br/><i>strict</i>, always loaded"] --> ENGINE
    ENGINE["Talon engine<br/>RETE-ish · reactive"] --> LLMR
    ENGINE --> DEF
    LLMR["llm_review<br/>fact-cached by head_sha"] --> DEF
    DEF["Defeasible resolution<br/>strict &gt; overrides &gt; priority"] --> EXPL
    EXPL["explain / audit"] --> OUT
    OUT(["→ actions[] + explain<br/>back to the bot"])

    ENGINE <--> ST[("talon-db<br/>facts · decisions<br/>subscriptions")]
    LLMR <--> ST
    EXPL --> ST

    classDef plug fill:#eef7ea,stroke:#4a8f3c,color:#0f2a0c
    classDef store fill:#ede7f6,stroke:#6a4fa3,color:#221040
    classDef edge fill:#f4f4f4,stroke:#8a8a8a,color:#333
    class RPC,RULES,BASE,ENGINE,DEF,LLMR,EXPL plug
    class ST store
    class IN,OUT edge
```

The seam: the plugin returns an **abstract action list**, the bot translates it
into API calls. Consequences worth stating —

- `talooner-plugin` is testable with zero GitHub fixtures.
- The bot holds no engine state, so it restarts freely.
- `rules plan` is not a separate code path. It's the same evaluation with the
  printer executor swapped in bot-side, driven by `mode: plan` here.

---

## 3. Flow — `evaluate_pr`

The bot's half is compressed; the full v1 entry-point flow (webhook verification,
write-access gate, token minting) is in `talooner/diagrams.md` §3.

```mermaid
sequenceDiagram
    autonumber
    participant Bot as talooner
    participant Plug as talooner-plugin
    participant DB as talon-db

    Bot->>Bot: extract facts at head_sha,<br/>load ruleset from BASE branch
    Bot->>Plug: action evaluate_pr —<br/>repo, pr, head_sha, facts JSON,<br/>ruleset text, mode

    Plug->>Plug: decode + validate request shape
    Plug->>Plug: parse/compile tenant ruleset<br/>+ strict base ruleset
    Plug->>DB: assert facts into (repo, pr) scope
    Note over Plug,DB: absent facts are RETRACTED —<br/>the bot always sends a full set
    Plug->>DB: mark PR subscribed

    Plug->>Plug: run engine
    opt a rule fires llm_review
        Plug->>Plug: see diagram 5
    end
    Plug->>Plug: defeasible resolution
    Plug->>DB: persist decision + explain
    Note over Plug,DB: persisted BEFORE responding —<br/>a bot crash still leaves a record

    Plug-->>Bot: actions[] + explain + warnings[]
    Bot->>Bot: translate to GitHub API calls
```

---

## 4. Flow — re-evaluation and retraction

Once invoked, the PR is subscribed. This is where reactive rules
(`when "pr.files_changed" changes`) earn their keep.

```mermaid
sequenceDiagram
    autonumber
    participant Bot as talooner
    participant Plug as talooner-plugin
    participant DB as talon-db

    Bot->>Plug: action is_subscribed — repo, pr

    alt not subscribed
        Plug-->>Bot: no
        Bot-->>Bot: drop — never reviewed unasked
    else subscribed
        Plug-->>Bot: yes
        Bot->>Bot: re-extract ALL facts at the new sha
        Note over Bot: full re-derivation, never deltas —<br/>this is what makes retraction work

        Bot->>Plug: action evaluate_pr — facts, ruleset, new_sha
        Plug->>DB: re-assert facts, retract stale ones
        Plug->>Plug: re-run engine from scratch
        Plug-->>Bot: actions[] + explain

        alt PR grew past 500 lines — approval no longer holds
            Note over Plug: the approve rule simply stops firing;<br/>no "un-approve" verb exists
            Bot->>Bot: dismiss the previous approving review
        end
    end
```

**Reversibility is not uniform**, and it is the bot that pays for it:

| Action | Reversible | On retraction |
|---|---|---|
| `approve` | yes | dismiss review, check → neutral |
| `block` | yes | check → success, dismiss REQUEST_CHANGES |
| `comment` | partly | edit to resolved state, never delete |
| `assign` / `require` | yes | remove assignee / withdraw request |
| `notify` | **no** | a sent Slack message stays sent |

`notify` being one-way is the reason it should be rare in a ruleset, and the
reason `validate_ruleset` is a reasonable place to warn about a `notify` in a
rule whose conditions churn.

---

## 5. Flow — `llm_review`, and why there is no cache layer

```mermaid
sequenceDiagram
    autonumber
    participant ENG as Talon engine
    participant DB as talon-db
    participant LLMR as llm_review
    participant Core as OpenTalon core
    participant API as LLM provider

    ENG->>LLMR: rule fired: llm_review(doc_url, diff)
    LLMR->>DB: fact at key<br/>(pr, head_sha, doc_url, prompt_version)?

    alt fact exists — same sha, already answered
        DB-->>LLMR: cached result
        LLMR-->>ENG: llm_review.result (no API call, no spend)
    else fact absent — new sha or new prompt version
        LLMR->>Core: review request (tenant credentials)
        Core->>API: completion
        API-->>Core: response
        Core-->>LLMR: constrained output
        Note over LLMR: enum only:<br/>match | mismatch | unclear |<br/>too_large | error
        LLMR->>DB: store as fact, pinned to head_sha
        LLMR-->>ENG: llm_review.result
    end

    ENG->>ENG: result is now an ordinary fact —<br/>rules decide, the model does not
```

The fact store **is** the cache. No separate layer, no invalidation logic: a new
commit produces a new sha, the fact is absent, the model runs again.

That gives the headline property: **same head sha + same base ruleset ⇒ same
actions, byte for byte.** A per-PR conversation is retained for continuity, but
it never changes an answer already recorded.

---

## 6. Where facts come from

```mermaid
flowchart LR
    GHAPI["GitHub API<br/><i>via the bot</i>"] -->|"diff stats, title, body,<br/>labels, check runs"| PRF["<b>pr.*</b><br/>built-in, always asserted"]
    CO[".github/CODEOWNERS"] --> USR["<b>user.*</b><br/>who owns this code"]
    MOD["modules.yaml"] --> USR
    MOD --> MODF["<b>module.*</b><br/>docs URL, owner"]
    TEAMS["teams.yaml"] --> TF["<b>team.*</b>"]
    RULES["rules.talon"] -->|"define blocks over<br/>pr.changed_files"| TOUCH["<b>pr.touches_*</b><br/>Talon-native path predicates"]
    PRF --> TOUCH
    REV["pull_request_review<br/>events"] --> REVF["<b>review.*</b>"]
    ENGINE["llm_review"] --> LLMF["<b>llm_review.*</b><br/>pinned to head_sha"]
    YOURCI["Tenant CI<br/>assert_facts"] --> CUSTOM["<b>preview.* screenshots.*<br/>dependency_scan.*</b>"]

    PRF --> STORE[("talon-db<br/>per-PR fact scope")]
    USR --> STORE
    MODF --> STORE
    TF --> STORE
    TOUCH --> STORE
    REVF --> STORE
    LLMF --> STORE
    CUSTOM --> STORE

    classDef repo fill:#fff8e6,stroke:#b58900,color:#432
    classDef danger fill:#fdeaea,stroke:#c0392b,color:#611
    class CO,MOD,TEAMS,RULES repo
    class CUSTOM danger
```

Everything yellow is **committed to the repo being reviewed**, under
`.github/talooner/`. The review policy is versioned, diffable, and unit-testable
with `.talon.test` — which is the claim no LLM-based reviewer can make.

The red box is the only namespace `assert_facts` may write. Enforcement lives in
this plugin: without it, a tenant CI workflow could POST `pr.tests_passing: true`
and defeat the entire ruleset. The bot filters too; this is the last line and the
one that owns the store. See `facts.md`, "Namespace enforcement lives here".

### One trap everyone working here must know about

A condition on an **unset** fact evaluates to *unknown*, not false — and
`not <unknown>` is unknown, not true. Otherwise a PR whose fact extraction failed
sails through `not is "critical_path"` and gets auto-approved.

This is a property of `talon-language`'s evaluator, not of this plugin, and it is
the first thing phase 0 verifies. See `facts.md`, "Unset is not false".
