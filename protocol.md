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
| `evaluate_pr` | `repo`, `pr`, `head_sha`, `facts` (JSON), `ruleset` (text), `mode` (`execute` \| `plan`) | `{actions: [...], explain: {...}, warnings: [...]}` |
| `is_subscribed` | `repo`, `pr` | `{subscribed: bool, since: ts}` |
| `set_subscription` | `repo`, `pr`, `state` | `{subscribed: bool}` |
| `assert_facts` | `repo`, `pr`, `facts` (JSON) | `{woke_rules: [...], actions: [...]}` — the custom-facts path |
| `validate_ruleset` | `ruleset` (text) | `{valid: bool, diagnostics: [...]}` — powers `talooner rules validate` |
| `explain_pr` | `repo`, `pr`, `head_sha` | `{explain: {...}}` — powers `@talooner /why` |
| `whoami` | — | `{tenant, quota, models, features}` |

`whoami` is the capability handshake, not just an identity check. The bot uses it
to know whether `llm_review` is even available before loading a ruleset that
depends on it — a ruleset using `llm_review` on a cluster without a configured
provider gets a validation warning at load time, not a runtime failure on the
first PR.

The action verbs returned inside `actions[]` are the abstract vocabulary — see
`engine.md`, "Abstract action vocabulary".

## Every action must set `user_only: true`

`Action.user_only` (`plugin.proto:173`) hides an action from the LLM and blocks
LLM-sourced calls. Talooner's actions must all set it.

Without it, any LLM running in that cluster — an unrelated conversation, a
different channel — could invoke `talooner.evaluate_pr` with arguments it made
up, and the bot would faithfully execute the returned actions against a real
repo. A model must never be able to reach into the decision path. The whole
design premise is that rules decide and the model answers questions; `user_only`
is what enforces that at the protocol level rather than by convention.

`read_only: true` on `is_subscribed`, `validate_ruleset`, `explain_pr`, `whoami`.
The rest mutate.

## Open: payload size

`args` values are strings in a unary gRPC call. A large PR's fact blob plus
ruleset text is realistically tens to low hundreds of KB — fine. `pr.diff` is the
risk: it can be megabytes.

Options, in preference order:

1. **Don't send the diff.** The engine only needs it for `llm_review`. Send a
   content hash plus a fetch handle, and have the bot serve the diff back on
   demand — but that inverts the dependency and gives the plugin a reason to know
   about GitHub. Rejected unless forced.
2. **Send it, size-capped**, with `pr.diff_truncated = true` asserted past the
   cap. Rules can match on the truncation. Simple, honest, keeps the seam.
3. `ExecuteBidi` streaming if the cap turns out too small.

**Leaning 2.** Cap is a config value; start at 1 MB. This is the "plugin protocol
fit for large payloads" item in the phase-0 table (`roadmap.md`).

## Cluster auth

Bot → cluster over gRPC, mTLS or bearer token depending on deployment. The
cluster API key is scoped to one tenant; if it leaks, an attacker can burn that
tenant's LLM budget and nothing else. It grants no GitHub capability — the bot
holds the GitHub App private key and the cluster never sees it.

The bot refuses to start if `whoami` fails. There is no degraded mode where it
reviews without the engine. Credential handling on the bot side is
[`talooner/auth.md`](https://github.com/opentalon/talooner/blob/main/auth.md).
