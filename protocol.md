# `talooner-plugin` — protocol

Diagram: `diagrams.md` §2b (internals), §1 (placement in the cluster).

## This is not a custom gRPC service

**Correction to earlier drafts, which sketched `EvaluatePR` as a bespoke rpc.**
It isn't one. OpenTalon's plugin host defines a fixed service in
`opentalon/proto/plugin.proto:9`:

```proto
service PluginService {
  rpc Init(PluginInitRequest) returns (google.protobuf.Empty);
  rpc Execute(ToolCallRequest) returns (ToolResultResponse);
  rpc Capabilities(google.protobuf.Empty) returns (PluginCapabilities);
  rpc RefreshCapabilities(google.protobuf.Empty) returns (PluginCapabilities);
  rpc ExecuteBidi(stream HostMessage) returns (stream PluginMessage);
}
```

A plugin does not add rpcs. It declares **actions**, and the host routes calls to
`Execute`. So `EvaluatePR` is an *action name*, not a method. This changes the
contract design in ways worth knowing before writing code:

```proto
message ToolCallRequest {
  string id = 1;
  string plugin = 2;
  string action = 3;
  map<string, string> args = 4;      // ← string values only
  map<string, CredentialHeader> credential_headers = 6;
}

message ToolResultResponse {
  string call_id = 1;
  string content = 2;                // human-readable
  string error = 3;
  string structured_content = 4;     // ← JSON, this is the real return channel
}
```

`args` is `map<string, string>`. Facts are structured data, so they travel as a
JSON blob in a single arg, and the decision comes back as JSON in
`structured_content`. `content` carries a human-readable summary — useful in logs
and when a person invokes the action directly.

## Actions to declare

| Action | Args | Returns (`structured_content`) |
|---|---|---|
| `evaluate_pr` | `repo`, `pr`, `head_sha`, `facts` (JSON), `ruleset` (text), `mode` (`execute` \| `plan`), `force` (bool) | `{actions: [...], explain: {...}, warnings: [...]}` — under `mode: plan`, a `plan[]` field and **no** `actions` key |
| `is_subscribed` | `repo`, `pr` | `{subscribed: bool, since: ts}` |
| `set_subscription` | `repo`, `pr`, `state` | `{subscribed: bool}` |
| `assert_facts` | `repo`, `pr`, `facts` (JSON) | `{accepted: [...], rejected: [...]}` — the custom-facts path, **store-only in v1** |
| `validate_ruleset` | `ruleset` (text) | `{valid: bool, diagnostics: [...]}` — powers `talooner rules validate` |
| `explain_pr` | `repo`, `pr`, `head_sha` | `{explain: {...}}` — powers `@talooner /why` |
| `whoami` | — | `{tenant, quota, models, features, protocol_version}` |

`whoami` is the capability handshake, not just an identity check. The caller uses
it to know whether `llm_review` is even available before loading a ruleset that
depends on it — a ruleset using `llm_review` on a cluster without a configured
provider gets a validation warning at load time, not a runtime failure on the
first PR.

`protocol_version` is new, and decision 1 is why. The action version is pinned in
each tenant's workflow file (`opentalon/talooner@v1`, or a sha); the plugin
version is whatever their cluster runs; nobody sees both. A version handshake
turns "strange behaviour on one repo" into one clear error at the top of a run.
The plugin refuses calls below its protocol floor rather than guessing, and the
action fails the run with a clear message on mismatch (`OPEN-QUESTIONS.md` B5).

### `force`

`--force` on the caller side (decision 18) maps to this arg. It bypasses the
`llm_review` fact cache for this evaluation only — the fact is recomputed and
overwritten at the same `(pr, head_sha, doc_url, prompt_version)` key.

Two rules it does not get to break: the per-PR call cap and the per-tenant budget
ceiling both still apply, and a `force` run that would exceed either asserts
`llm_review.result = "error"` exactly as an ordinary run would. `force` is a
cache-bypass, not a budget override — otherwise it becomes the thing a
frustrated maintainer spams at 2am.

### `assert_facts` returns no actions

Earlier drafts had it return `{woke_rules, actions}`. Under decision 20 there is
nobody to execute those actions: the caller is a workflow run that finished long
before the tenant's CI POSTed the fact, and this plugin holds no GitHub
credential.

So the v1 return is an acknowledgement — which facts were accepted, which were
rejected by namespace enforcement, and why. Facts are read by the next
`evaluate_pr`. Returning an action list that nothing executes would be a trap:
the first person to wire it up would assume the comment got posted.

When phase 4 adds dispatch-driven wake, `actions[]` can come back — but then the
thing that woke the engine is a workflow run that is still alive to act on it.

The action verbs returned inside `actions[]` are the abstract vocabulary — see
`engine.md`, "Abstract action vocabulary".

## Every action must set `user_only: true`

`Action.user_only` (`plugin.proto:173`) hides an action from the LLM and blocks
LLM-sourced calls. Talooner's actions must all set it.

Without it, any LLM running in that cluster — an unrelated conversation, a
different channel — could invoke `talooner.evaluate_pr` with arguments it made
up, and the next run would faithfully execute the returned actions against a real
repo. A model must never be able to reach into the decision path. The whole
design premise is that rules decide and the model answers questions; `user_only`
is what enforces that at the protocol level rather than by convention.

`read_only: true` on `is_subscribed`, `validate_ruleset`, `explain_pr`, `whoami`.
The rest mutate.

## Payload size

`args` values are strings in a unary gRPC call. A large PR's fact blob plus
ruleset text is realistically tens to low hundreds of KB — fine. `pr.diff` is the
risk: it can be megabytes.

**Decision: send it, size-capped**, with `pr.diff_truncated = true` asserted past
the cap. Rules can match on the truncation. Simple, honest, keeps the seam. Cap
is a config value; start at 1 MB.

`pr.diff_truncated` is not optional bookkeeping. Under the A1 semantics an unset
fact reads as false, so without it "the diff had no problems" and "we never saw
the diff" are the same value to the engine — the A1 failure mode landing on the
one input most likely to hit a size limit.

The two rejected options, so they aren't re-proposed:

1. **Don't send the diff.** The engine only needs it for `llm_review`. Sending a
   content hash plus a fetch handle inverts the dependency and gives the plugin a
   reason to know about GitHub.
2. **`ExecuteBidi` streaming.** Reserved for if the cap turns out too small.

### The ceiling is 32 MiB, and it isn't ours

Phase 0 found that no message-size options were set on any host↔plugin path in
`opentalon`, so gRPC's 4 MiB default receive limit governed every call and the
failure past it was a transport error naming no field. Fixed in core:
[`opentalon#325`](https://github.com/opentalon/opentalon/issues/325), landed
2026-08-07 in 4cbc14d. Limits are now set symmetrically on server and client
(`opentalon/internal/grpclimit`) — **32 MiB by default**, overridable with
`OPENTALON_GRPC_MAX_MSG_BYTES`.

Two things that follow:

- **Assume the default.** The override is an operator knob on the cluster and has
  to be set on both ends; a plugin that assumes a raised ceiling breaks on a
  stock deployment.
- **The bot-side cap stays.** 1 MiB of diff against a 32 MiB transport ceiling is
  not headroom management — it is so the failure is our error message naming
  `pr.diff`, rather than a `ResourceExhausted` with a byte count. It is also what
  makes `pr.diff_truncated` a fact the tenant can write rules against.

## Cluster auth

Caller → cluster over gRPC, mTLS or bearer token depending on deployment. The
cluster API key is scoped to one tenant; if it leaks, an attacker can burn that
tenant's LLM budget and nothing else. It grants no GitHub capability — the caller
holds a `GITHUB_TOKEN` that Actions minted for one job, and the cluster never
sees it.

The run fails fast if `whoami` fails. There is no degraded mode where it reviews
without the engine. Credential handling on the caller side is
[`talooner/auth.md`](https://github.com/opentalon/talooner/blob/main/auth.md).

### The caller is now off-network

This is the operational change decision 1 forces on this repo. Before, the caller
was a process on the same box. Now it's a container in GitHub's infrastructure,
so:

- **The endpoint is exposed.** Public gRPC + TLS + API key is the default;
  self-hosted runners are the alternative for tenants who won't expose it. See
  `deployment.md`, "Exposing the cluster".
- **No connection reuse.** Every run dials fresh, does `whoami`, does its work,
  and disconnects. Nothing amortises across events, so a handshake-heavy setup
  (mTLS with a slow CA check) is paid on every push to every PR. Keep the
  handshake cheap.
- **Latency is now WAN latency**, not loopback. Fine for a reviewer — the run
  already spent 10–30s starting — but it rules out chatty designs. One
  `evaluate_pr` per run, not a conversation.
- **The client is untrusted infrastructure.** The API key sits in a repo secret
  and is exposed to any workflow in that repo that can read secrets. Tenants
  should scope one key per repo, or accept that a repo's write-access holders
  collectively hold it. Rate limiting per key belongs here, not in the caller.
