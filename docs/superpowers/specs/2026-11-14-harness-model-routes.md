<!-- Coordinator preface, 2026-11-14. The spec below was written against a
`task-workflow` branch that does not exist on this machine. Treat §12 as the
implementation plan and build it fresh on feat/task-plans (worktree
/Users/ceo/ao-task-plans) on top of 74fcd574b2. Rulings:

R-A  Same branch and worktree as Task Plans, one commit per milestone (M1
     foundations: §12.2 §12.3 §12.4; M2 Claude Code §12.5; M3 Codex §12.6 and
     OpenCode §12.7; M4 app §12.8, locales, docs, handoff). Never push.
R-B  `codex_account_service.go` does not exist here; find where the Codex
     account readiness override skips custom providers and keep that behaviour.
R-C  Locales are en plus de, es, fr, ja, ko, pt-BR, ru, zh-CN (nine files).
R-D  The probe must honour the ctx deadline the readiness coordinator already
     applies and read AO_MODEL_ROUTE_PROBE at check time, not at init.
R-E  Logs and diagnostics carry hostname[:port] only; never a URL with
     userinfo, path or query, never a header value, never a body.
R-F  App surfaces follow DESIGN.md (sentence case, no new panels or glows,
     reuse the existing harness row and tab primitives).
R-G  C1 is proven by leaving every existing auth test untouched except the
     `clearClaudeCredentialEnv` addition named in §12.10; new behaviour gets
     new tests. The OpenCode 2 MCP change is the only deliberate verdict
     change and needs the failing-before test named in §12.10.
R-H  Where this spec conflicts with the code on main, the code's structure
     wins and the spec is amended in place (append a §13 "Rulings" like the
     intake-modes spec). Where it is silent, choose the smallest change.
-->

# Harness model routes: identifying gateway, cloud and keyless logins

Status: being rebuilt on `feat/task-plans` (milestones M1–M4, see the preface). Section 12 is the code map from the first implementation; section 13 records where this build differs.
Applies to: Claude Code, Codex, OpenCode 1 and OpenCode 2 harnesses (local hosts)

## 1. Problem

AO decides whether a harness "is signed in" by asking about the provider's own
login: a Claude subscription or Anthropic key, a ChatGPT login or OpenAI key, an
OpenCode stored credential. Many machines do not use those. They send model
traffic through something else:

- a cloud provider (AWS Bedrock, Google Vertex AI, Azure AI Foundry);
- an HTTP gateway or proxy (LiteLLM, Cloudflare AI Gateway, an internal company
  gateway, OpenRouter, a local Ollama or LM Studio);
- an endpoint that needs **no key at all**, because access is granted by the
  network (VPN, zero-trust client such as Cloudflare WARP, mTLS) or by ambient
  cloud credentials.

Today those machines get misleading results:

| Harness | Setup | Current verdict | Why it is wrong |
|---|---|---|---|
| Claude Code | `CLAUDE_CODE_USE_BEDROCK=1` (any base URL, `CLAUDE_CODE_SKIP_BEDROCK_AUTH`) | `configured`, "could not verify", no indication of where traffic goes | The user cannot tell AO saw Bedrock at all |
| Claude Code | `ANTHROPIC_BASE_URL` gateway with no key | `unknown` (credential missing) | A keyless gateway is a working setup |
| Codex | `model_provider = "<custom>"` in `config.toml`, no `env_key` | `unknown`, recorded as an inconclusive *failure* and retried | The provider works; AO just has no rule for it |
| OpenCode 2 | Only MCP servers have stored OAuth credentials | `authorized` | MCP tool credentials are not model credentials |
| OpenCode 1/2 | Custom providers in `opencode.json` with `baseURL` and empty `apiKey` | Depends on unrelated env keys | The configured providers are never looked at |

## 2. Goals and non-goals

Goals

1. Identify, for each harness, **which model route(s)** it is configured to
   use: kind, API family, endpoint host, credential mode, and where that was
   configured.
2. Verify a route when a **side-effect-free, credential-faithful** check exists
   (listing models), so keyless and gateway setups can read as signed in.
3. When a route cannot be verified, say so precisely ("Using AWS Bedrock via
   gw.example.com, can't be verified without a model call"), instead of a
   generic "unverified".
4. Show the route in the app (harness row, harness page, copied health report).
5. Work on any computer: rules derive only from each harness's documented
   configuration, never from a specific company, host or variable value.

Non-goals

- No new login flows, no storing credentials, no editing harness config.
- No model invocation (no tokens spent) to verify anything.
- No change to remote/cloud harness hosts (they keep their own checks).
- No change to launch: spawn remains the authoritative check, and an
  identified-but-unverified route never blocks a launch.

## 3. Compatibility rules (must hold)

C1. Every existing first-party path is unchanged: Claude subscription, setup
    token, `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` with a key (existing
    gateway validator), Codex ChatGPT/API-key login and AO Codex accounts,
    OpenCode stored provider credentials and environment keys, saved default
    accounts. Their verdicts, account identity and diagnostics stay identical.
C2. The change is additive at every step: a route rule may turn `unknown` into
    `configured` or `authorized`, and may turn a verdict into `unauthorized`
    only on a definite rejection from the very endpoint the harness uses.
    It never downgrades an existing `authorized` verdict that came from an
    unchanged rule. (Exception, a bug fix: OpenCode 2 no longer counts MCP
    server credentials as a model login.)
C3. The API change is additive (`authentication.routes`, new diagnostic codes).
    Old clients ignore it.
C4. Readiness derivation is unchanged: `configured` and `unknown` derive to
    `unknown` readiness (never blocks), `authorized` to `ready`.

## 4. Model route

```
AgentModelRoute {
  name?:        string   // config id: Codex model_provider, OpenCode provider id
  kind:         first_party | gateway | cloud | local
  apiFamily:    anthropic | openai | openai_compatible | bedrock | vertex | foundry | google | other
  host?:        string   // hostname[:port] only; never scheme, path, query or userinfo
  credential:   account | api_key | token | cloud_credentials | none | unknown
  credentialSource?: string // env var NAME or home-relative config path; never a value
  configSource?: string  // home-relative file or env var name the route came from
  verification: verified | rejected | unverifiable | inconclusive | access_challenge | unreachable | not_attempted | disabled
  isDefault?:   bool     // the route the harness's default model uses, when known
}
AgentAuthenticationObservation.routes?: AgentModelRoute[]   // max 16
```

- `kind=local`: host is loopback (`localhost`, `127.0.0.0/8`, `::1`).
- `kind=gateway`: an explicit base URL that is not the provider's own default.
- `kind=cloud`: Bedrock, Vertex, Foundry selected by the harness's own switch.
- `credential=none`: the harness is configured to send no key (empty key, skip
  auth flag, provider with no `env_key`). Access is then decided by the network
  or ambient identity; AO cannot see which and does not guess.
- Every string is sanitized like account identity fields (trimmed, no control
  characters, bounded length). Hosts are re-derived from the parsed URL.

## 5. Verification probe

A route is verified only by one of these requests, sent to the route's own base
URL, with exactly the credential the harness would send there and nothing else:

| apiFamily | Request | Credential header (only if the harness sends one) |
|---|---|---|
| openai, openai_compatible | `GET {baseURL}/models` | `Authorization: Bearer <key>` |
| anthropic (gateway) | `GET {baseURL}/v1/models` (or `{baseURL}/models` when baseURL already ends in `/v1`) | `x-api-key` / `Authorization: Bearer`, plus `anthropic-version` |
| bedrock, vertex, foundry, google, other | none: `unverifiable` | — |

Interpretation:

| Response | verification | Effect on verdict |
|---|---|---|
| 2xx, JSON object with a `data` or `models` array | `verified` | may produce `authorized` |
| 401 / 403 with a JSON body | `rejected` | may produce `unauthorized` (only if no other rule authorizes) |
| 3xx, or 401/403 with HTML/non-JSON (login page, zero-trust challenge) | `access_challenge` | `configured`, next step: "connect to the network or VPN that grants access" |
| other status, wrong shape | `inconclusive` | `configured` |
| DNS/TCP/TLS failure, timeout | `unreachable` | `configured` |

A model listing can be public. When the harness sends a key, AO repeats the
listing **without** the key; if that also succeeds the listing proves nothing
about the key and the result is `inconclusive`. A keyless route is verified by
its listing alone, because the listing is exactly what the harness gets.

Safety:

- A key is never sent over plain `http` to a non-loopback host for a check
  (`inconclusive` instead).

- Only `GET`, only `http`/`https`, redirects are never followed, 3 s timeout,
  response body read capped at 1 MiB and never stored or logged.
- No probe for a route whose credential is `cloud_credentials` (AO would have to
  sign requests with the user's cloud identity).
- A verdict is cached per (base URL, credential fingerprint) for 5 minutes and
  dropped on "Recheck" and on a runtime 401, like the existing Claude cache.
- `AO_MODEL_ROUTE_PROBE=off` disables all probes (verification `disabled`).
- Tests inject the HTTP client; no unit test can reach the network.

## 6. Verdict aggregation

Per harness, after the existing (unchanged) rules have produced `existing`:

1. If `existing` is `authorized` or `unauthorized`, keep it; routes are added
   for display only (C1).
2. Otherwise, over the identified routes (the default route first when known):
   - any `verified` → `authorized`, account method/identity from that route;
   - else all probed routes `rejected` and no unprobed route → `unauthorized`;
   - else any identified route → `configured` (reason names the route);
   - else `existing`.

## 7. Detection rules per harness

All rules read the same environment the harness's sessions get (AO's environment
plus AO's per-harness overrides) and the harness's own config files. AO never
executes helpers named in config (`apiKeyHelper`, `awsAuthRefresh`,
`{file:...}` is read only if it is a plain file under the user's home).

### 7.1 Claude Code

Environment = process env, overlaid by `env` from `~/.claude/settings.json`
(and `CLAUDE_CONFIG_DIR`), exactly as the existing resolver does.

| Switch | Route |
|---|---|
| `CLAUDE_CODE_USE_BEDROCK` truthy | `cloud`, `bedrock`; host from `ANTHROPIC_BEDROCK_BASE_URL` else `bedrock-runtime.<AWS_REGION>.amazonaws.com`; credential `none` if `CLAUDE_CODE_SKIP_BEDROCK_AUTH` truthy, `token` if `AWS_BEARER_TOKEN_BEDROCK` set, else `cloud_credentials` (source `AWS_PROFILE` when set) |
| `CLAUDE_CODE_USE_VERTEX` truthy | `cloud`, `vertex`; host from `ANTHROPIC_VERTEX_BASE_URL`; credential `none` if `CLAUDE_CODE_SKIP_VERTEX_AUTH`, else `cloud_credentials` |
| `CLAUDE_CODE_USE_FOUNDRY` truthy | `cloud`, `foundry`; host from `ANTHROPIC_FOUNDRY_BASE_URL` or `<ANTHROPIC_FOUNDRY_RESOURCE>.services.ai.azure.com`; credential `none` if `CLAUDE_CODE_SKIP_FOUNDRY_AUTH`, `api_key` if `ANTHROPIC_FOUNDRY_API_KEY`, else `cloud_credentials` |
| `ANTHROPIC_BASE_URL` set (no cloud switch) | `gateway` (or `local`), `anthropic`; credential from the existing ladder, `none` if no credential |
| none of the above | first party: no route is recorded (the existing account line already says it; C1) |

Verdict additions: a cloud route records `verification=unverifiable` and, when
the existing ladder ends in `unknown`, yields `configured` (C2). A keyless
gateway is probed (section 5) instead of ending at `credential_missing`. A
gateway with a key keeps the existing validator.

### 7.2 Codex

Config = `$CODEX_HOME/config.toml` (default `~/.codex`): `model_provider`, the
active `profile`'s `model_provider`, and `[model_providers.<id>]` with
`base_url`, `env_key`, `requires_openai_auth`, `wire_api`, `http_headers`,
`env_http_headers`.

| Provider | Route |
|---|---|
| unset or `openai`, or `requires_openai_auth = true` | first party: no route recorded; existing `codex login status` and Codex accounts (C1) |
| custom with `base_url` | `gateway`/`local`, `openai` (`wire_api = "responses"`) or `openai_compatible`; credential `api_key` from `env_key` (present or missing), `token` when `env_http_headers` maps an auth header, else `none` |
| built-in `oss` / `ollama` / `lmstudio` without `base_url` | `local`, `openai_compatible`, host `localhost:<default port>`, credential `none` |

Verdict: custom routes are probed with exactly the key Codex would send
(`env_key` value) or with no key. A missing `env_key` variable keeps today's
`unknown` (never worse) with diagnostic `route_credential_missing`.

### 7.3 OpenCode 1 and OpenCode 2

Config layers (lowest to highest, both majors): global
`$XDG_CONFIG_HOME/opencode/{config.json,opencode.json,opencode.jsonc}`,
`OPENCODE_CONFIG`, `OPENCODE_CONFIG_DIR/opencode.json(c)`,
`OPENCODE_CONFIG_CONTENT`, managed config. Project files inside the repository
are not read (they vary per worktree). JSONC comments and trailing commas are
accepted.

For each entry in `provider` (later layers override earlier ones by id):

| Provider entry | Route |
|---|---|
| `options.baseURL` set | `gateway`/`local`; apiFamily from `npm`: `@ai-sdk/openai` → `openai`, `@ai-sdk/openai-compatible` → `openai_compatible`, `@ai-sdk/anthropic` → `anthropic`, `@ai-sdk/amazon-bedrock` → `bedrock`, `@ai-sdk/google*` → `google`, else `other`; credential `api_key` if `options.apiKey` is a non-empty literal or `{env:VAR}` that resolves, `none` if empty/absent and no stored credential for that id, else `unknown` |
| no `baseURL` | not a new route (built-in providers keep the existing credential rules) |

`isDefault` marks the provider of the root `model` (`provider/model`) when a
certain layer sets it.

Stored-credential fix (OpenCode 2, C2 exception): in
`opencode auth list --format json`, integrations whose id starts with `mcp_`
are MCP servers and do not count as model credentials. OpenCode 1 already keeps
MCP OAuth in a separate file and is unchanged.

Verdict: the existing rules run first (C1). Otherwise section 6 over the
provider routes; probes run concurrently, at most 8, within the auth budget.

## 8. App

- Harness row and page: when `routes` exist, a route line is shown in place of
  (or after) the account line, for example:
  - "AWS Bedrock via gw.example.com · no key (network access)"
  - "Custom provider fp-gw · OpenAI via gw.example.com · no key"
  - "3 providers via gw.example.com · 2 verified"
- Status label: `authorized` → "Logged in" (existing); `configured` from a
  route → "Configured, can't be verified" for `unverifiable`, "Can't reach
  provider" for `unreachable`, "Needs network access" for `access_challenge`.
- Copied health report: one block per route with every field and its
  verification (English, like the rest of the report).
- All new strings exist in every locale catalog.

- Harnesses with a Subscriptions panel (Claude Code, Codex) get a "Model route"
  group above it, and, when no account credential reaches the route (cloud
  provider, keyless gateway, or a gateway with its own key), the note "Model
  requests use this route, so the subscriptions below aren't used for them."

## 9. Diagnostics (new codes)

`route_identified`, `route_verified`, `route_rejected`, `route_unverifiable`,
`route_inconclusive`, `route_access_challenge`, `route_unreachable`,
`route_probe_disabled`, `route_credential_missing`. Each has an authored
reason and next step; none echoes response bodies or credential values.

## 10. Test plan

Unit (Go, temp HOME/XDG dirs, injected HTTP server, no network):

- Claude: Bedrock skip-auth with base URL → `configured`, route bedrock/none/host;
  Bedrock with `AWS_PROFILE` → cloud_credentials; Vertex/Foundry; keyless
  `ANTHROPIC_BASE_URL` → probe verified → `authorized`; probe 403 HTML →
  `configured` + access_challenge; first-party paths unchanged (existing tests).
- Codex: custom provider keyless 200 → `authorized`; `env_key` present → Bearer
  sent; `env_key` missing → `unknown` + `route_credential_missing`; 401 JSON →
  `unauthorized`; profile selection; `requires_openai_auth` keeps login status.
- OpenCode: JSONC parse; layer override; apiKey literal/`{env:}`/empty; MCP
  integrations excluded (v2); default-model route first; any verified →
  `authorized`; Bedrock-family route → `unverifiable`.
- Probe: no redirects followed, body cap, timeout, cache hit and invalidation,
  `AO_MODEL_ROUTE_PROBE=off`, host sanitization (userinfo/query stripped).
- Coordinator: routes are attached for every verdict and sanitized.

Frontend (vitest): route descriptions and status labels for each verification;
health text includes routes; locale coverage.

## 11. Manual check on any computer

1. `ao agent ls` (or Settings → Harnesses) and look at the three harnesses.
2. Expected by setup:
   - Bedrock/Vertex/Foundry Claude Code: "Configured, can't be verified" with
     the cloud route and host.
   - Keyless gateway (Codex custom provider, OpenCode baseURL providers, Claude
     `ANTHROPIC_BASE_URL`): "Logged in" when the gateway answers its model list;
     "Needs network access" when off VPN/zero-trust.
   - Plain subscription/API-key logins: unchanged.
3. "Copy health report" shows each route with its verification.

## 12. Implementation details

Everything below is uncommitted on branch `task-workflow`. Paths are relative
to the repository root. New files are marked (new).

### 12.1 Data flow

```
adapter AuthStatus(ctx)                       (Claude Code / Codex / OpenCode 1 / OpenCode 2)
  ├─ identifies its route(s) from the harness's own config
  ├─ modelroute.Check(ctx, Request)           GET <baseURL>/models, credential-faithful
  └─ modelroute.Record(ctx, route, result)    → ports.RecordModelRoute + a "model_route" diagnostic
        │  (collected in the ports.AuthEvidence carried by ctx)
readinessCoordinator.checkAuthentication
  ├─ verdict from the adapter (authorized / configured / unknown / unauthorized)
  ├─ observation.Routes = domain.SanitizedRoutes(evidence.Routes())
  └─ configured + routes → reason names the route (describeRoutes)
GET/POST /api/v1/agents/readiness[/ensure] → authentication.routes[]
App: describe-harness.ts → Account tab, Health tab, copied diagnostics
```

No storage or migration change: readiness observations are in memory only.

### 12.2 Domain and API

`backend/internal/domain/agent_model_route.go` (new)

- Types `AgentModelRouteKind`, `AgentModelRouteFamily`,
  `AgentModelRouteCredential`, `AgentModelRouteVerification` with the values
  in section 4.
- `AgentModelRoute` struct: `Name`, `Kind`, `APIFamily`, `Host`, `Credential`,
  `CredentialSource`, `ConfigSource`, `Verification`, `HTTPStatus`, `IsDefault`.
- `RouteHost(raw)`: `hostname[:port]` of an absolute http(s) URL; scheme,
  userinfo, path, query and fragment are dropped; anything else returns "".
- `IsLoopbackHost(host)`: `localhost`, `127.0.0.0/8`, `::1`.
- `AgentModelRoute.Sanitized()`: trims and bounds strings (same rules as
  account identity), drops a host containing `/?#@ `, maps unknown enum values
  to `other` / `unknown` / `not_attempted`, rejects an unknown kind.
  `SanitizedRoutes` applies it and caps the list at `MaxAgentModelRoutes` (16).
- `AgentModelRouteVerification.DiagnosticCode()` maps each verification to
  its `route_*` diagnostic code.

`backend/internal/domain/agent_readiness.go`

- `AgentAuthenticationObservation.Routes []AgentModelRoute` (`json:"routes,omitempty"`).

`backend/internal/domain/agent_auth_diagnostic.go`

- Authored reason and next step for `route_identified`, `route_verified`,
  `route_rejected`, `route_unverifiable`, `route_inconclusive`,
  `route_access_challenge`, `route_unreachable`, `route_probe_disabled`,
  `route_credential_missing`.

`backend/internal/httpd/apispec/specgen/build.go`

- Schema name mapping `DomainAgentModelRoute` → `AgentModelRoute`.
- Regenerated with `npm run api`: `backend/internal/httpd/apispec/openapi.yaml`
  and `frontend/src/api/schema.ts` gain `AgentModelRoute` and
  `AgentAuthenticationObservation.routes`. Regeneration is stable.

### 12.3 Evidence collection and readiness

`backend/internal/ports/agent.go`

- `AuthEvidence` gains a `routes` slice.
- `RecordModelRoute(ctx, route)`: records into the evidence on `ctx`; a route
  with the same kind, family, host and name replaces the earlier one; bounded
  at 16. No-op without a collector.
- `(*AuthEvidence).Routes()`: copy of the recorded routes.

`backend/internal/service/agent/readiness_coordinator.go`

- In `checkAuthentication`'s deferred block, the sanitized routes are attached
  to the observation for every verdict, including failures.
- For a `configured` verdict with routes, the reason becomes
  "<Harness> is configured to use <route>. Orchestrator could not verify it
  without starting a session."

`backend/internal/service/agent/model_routes.go` (new)

- `describeRoutes(routes)`: English text for that reason, for example
  "AWS Bedrock via gw.example with no key" or "several providers via gw.example".

`backend/internal/service/agent/readiness.go`

- `InvalidateAgentAuthentication` (used by "Check again" and runtime auth
  failures) also calls `modelroute.InvalidateCache()`.

### 12.4 Route check

`backend/internal/adapters/agent/modelroute/probe.go` (new)

- `Request{BaseURL, Family, Headers}`, `Result{Verification, HTTPStatus,
  Cached, CheckedAt, Duration}`.
- `ListURL(baseURL, family)`: `<base>/models` for `openai` and
  `openai_compatible`; `<base>/v1/models` for `anthropic` (or `<base>/models`
  when the base already ends in `/v1`); "" for every other family (they are
  `unverifiable`).
- `Check(ctx, req)`:
  1. `AO_MODEL_ROUTE_PROBE=off` → `disabled`.
  2. No listing URL → `unverifiable`.
  3. Cached result (5 minutes, keyed by URL plus a SHA-256 of the auth
     header values) → returned with `Cached`.
  4. Headers present and the target is plain `http` off loopback →
     `inconclusive` without sending anything.
  5. `GET` with `Accept: application/json` (plus `anthropic-version` for
     Anthropic) and the given headers; 3-second timeout; redirects not followed;
     body read capped at 1 MiB and never kept.
  6. If verified with a key, the same request is repeated without it; a
     second success downgrades to `inconclusive` (public listing).
  7. Cancelled checks are not cached.
- `classify`: 3xx → `access_challenge`; 401/403 with JSON → `rejected`;
  401/403 without JSON → `access_challenge`; 2xx JSON with a `data` or
  `models` array → `verified`; 2xx HTML → `access_challenge`; anything else →
  `inconclusive`; transport error → `unreachable`.
- `Record(ctx, route, result)`: sets verification and status, calls
  `ports.RecordModelRoute`, and records a `model_route` diagnostic whose
  source is `name · host`, never a credential.
- `Kind(host)`: `local` for loopback, else `gateway`.
- `Status(routes)`: section 6 aggregation (any verified → authorized; all
  rejected → unauthorized; otherwise configured; none → not ok).
- `Client` is a package variable so tests can replace the transport.

`backend/internal/adapters/agent/jsonc/jsonc.go` (new)

- `Strip(raw)`: removes JSONC comments and trailing commas. It was moved
  unchanged from `modelcatalog`, whose `stripJSONC` now delegates to it.

### 12.5 Claude Code

`backend/pkg/agentcreds/claude_route_env.go` (new)

- `ClaudeRouteEnv(ctx, opts)`: lookup in the order the existing resolver and
  Claude Code apply: the explicit launch environment, then the `env` block of
  the user's own `settings.json` (in `CLAUDE_CONFIG_DIR` or `~/.claude`) when it
  defines the key (an empty entry shadows), then the process environment. Route
  keys only (ruling R18):
  `CLAUDE_CODE_USE_{BEDROCK,VERTEX,FOUNDRY}`, `ANTHROPIC_BASE_URL`,
  `ANTHROPIC_{BEDROCK,VERTEX,FOUNDRY}_BASE_URL`, `ANTHROPIC_FOUNDRY_RESOURCE`,
  `CLAUDE_CODE_SKIP_{BEDROCK,VERTEX,FOUNDRY}_AUTH`, `AWS_REGION`,
  `AWS_DEFAULT_REGION`, `AWS_PROFILE`, `CLOUD_ML_REGION`, and (presence only)
  `AWS_BEARER_TOKEN_BEDROCK`, `ANTHROPIC_FOUNDRY_API_KEY`. Project settings
  files are never read for these. JSON booleans and numbers are accepted as
  flag values.
- `EnvTruthy(value)` and `ResolveOptions.Getenv(name)` are exported helpers.
- The existing credential resolver is unchanged: it still does not take cloud
  switches from settings files.

`backend/internal/adapters/agent/claudecode/route.go` (new)

- `claudeModelRoute(ctx, resolved)` builds the route from the provider the
  existing ladder already resolved (environment switch or the CLI's
  `apiProvider`):
  - Bedrock: host from `ANTHROPIC_BEDROCK_BASE_URL`, else
    `bedrock-runtime.<AWS_REGION>.amazonaws.com`; credential `none` with
    `CLAUDE_CODE_SKIP_BEDROCK_AUTH`, `token` with `AWS_BEARER_TOKEN_BEDROCK`,
    else `cloud_credentials` (source `AWS_PROFILE` when set).
  - Vertex: host from `ANTHROPIC_VERTEX_BASE_URL`, else
    `<CLOUD_ML_REGION>-aiplatform.googleapis.com` (or `aiplatform.googleapis.com`
    for `global`); `none` with the skip flag, else `cloud_credentials`.
  - Foundry: host from `ANTHROPIC_FOUNDRY_BASE_URL`, else
    `<resource>.services.ai.azure.com`; `none` with the skip flag, `api_key`
    with `ANTHROPIC_FOUNDRY_API_KEY`, else `cloud_credentials`.
  - Gateway (`ANTHROPIC_BASE_URL`): kind gateway/local, family `anthropic`,
    credential from the ladder's resolved credential, or `none`.
  - First party: no route.
  - `ConfigSource` is the environment variable when it is set in the
    environment, else `~/.claude/settings.json`.
- `routeVerificationFromStep(code)` maps the existing provider validator's
  outcome (`provider_accepted`, `provider_rejected`, ...) to a verification.

`backend/internal/adapters/agent/claudecode/auth.go`

- After rungs 1–2, in `authStatusAndDiagnostics`:
  - cloud route → recorded as `unverifiable` (no request);
  - gateway without a credential → `modelroute.Check` without any key; a
    verified or rejected result returns `authorized` / `unauthorized`;
  - gateway with a credential → the existing validator's outcome is recorded
    on the route (no second request).
- After rung 4: an identified route turns a final `unknown` into `configured`.
- Every first-party path returns the same verdicts as before.

### 12.6 Codex

`backend/internal/adapters/agent/codex/codex.go`

- `codexModelProvider` now also parses `base_url`, `wire_api`,
  `http_headers` and `env_http_headers`, and carries the provider `Name`.
- `AuthStatus` delegates custom providers to `customProviderStatus`; the
  OpenAI path (`codex login status`) is unchanged.

`backend/internal/adapters/agent/codex/codex_route.go` (new)

- `customProviderStatus(ctx, provider)`:
  - Base URL from `base_url`, else the built-in local defaults (`oss`,
    `ollama`: `http://localhost:11434/v1`; `lmstudio`: `http://localhost:1234/v1`).
  - Family `openai` when `wire_api = "responses"`, else `openai_compatible`.
  - Credential: `none` by default; `token` for an auth header in
    `http_headers` or `env_http_headers`; `api_key` for `env_key`. Headers are
    built exactly as Codex sends them (`Authorization: Bearer <env_key value>`).
  - `env_key` named but unset → route recorded as `not_attempted`,
    `route_credential_missing` diagnostic, verdict stays `unknown`.
  - No resolvable host → route recorded as `inconclusive`, verdict from the
    previous rule (`legacyCustomProviderStatus`: configured with a present
    key, else unknown).
  - Otherwise the route is checked and `modelroute.Status` decides.
- The Codex account-readiness override already skipped custom providers
  (`codex_account_service.go`), so this adapter result is what readiness uses.

### 12.7 OpenCode 1 and OpenCode 2

`backend/internal/adapters/agent/opencode/route.go` (new)

- `ConfigHome` (variable: `XDG_CONFIG_HOME` or `~/.config`) and
  `ManagedConfigDirs` (variable; macOS `/Library/Application Support/opencode`,
  Linux `/etc/opencode`, Windows `%ProgramData%\opencode`). Both are variables so
  tests never read the developer's config.
- `configLayers`: global `config.json`, `opencode.json`, `opencode.jsonc`;
  `OPENCODE_CONFIG`; `OPENCODE_CONFIG_DIR/opencode.json(c)`;
  `OPENCODE_CONFIG_CONTENT`; managed config. Files are capped at 4 MiB.
  Project files are not read.
- `configuredProviders`: merges `provider.<id>` across layers (`npm`, `env`,
  `options.baseURL`, `options.apiKey`), keeps entries with a `baseURL`, and
  sorts the root `model`'s provider first, then by id.
- `familyForNPM`: `@ai-sdk/openai` → openai, `@ai-sdk/openai-compatible` or
  none → openai_compatible, `@ai-sdk/anthropic` → anthropic,
  `@ai-sdk/amazon-bedrock` → bedrock, `@ai-sdk/google-vertex*` → vertex,
  `@ai-sdk/google*` → google, `@ai-sdk/azure` → foundry, else other.
- `checkProvider`: key from a literal `apiKey`, `{env:VAR}` (unset →
  `not_attempted` + `route_credential_missing`), the provider's `env` list,
  or none. `{file:...}` keys and providers with a stored OpenCode credential
  are recorded as `not_attempted` (AO cannot see the value). Anthropic routes
  send `x-api-key`; others `Authorization: Bearer`.
- `CheckProviderRoutes(ctx, stored)`: up to 16 providers, at most 8 checks
  at a time, each collecting into its own evidence, then replayed in sorted
  order so routes and diagnostics are deterministic.
- `WithRoutes(existing, routes)`: a definite existing verdict stands;
  otherwise the routes decide (section 6).

`backend/internal/adapters/agent/opencode/opencode.go` (OpenCode 1)

- The previous `AuthStatus` body is now `storedOrEnvAuthStatus` (unchanged).
- `AuthStatus` = `WithRoutes(storedOrEnvAuthStatus, CheckProviderRoutes(ctx, storedProviderIDs()))`.
- `storedProviderIDs()`: provider ids with a non-empty entry in OpenCode 1's
  `auth.json`.

`backend/internal/adapters/agent/opencodev2/opencodev2.go` (OpenCode 2)

- Parses `opencode auth list --format json` integrations with their `id` and
  connection `type`.
- **Bug fix:** integrations whose id starts with `mcp_` (MCP servers' OAuth)
  no longer count as a model login.
- Integrations with `credential` connections populate `stored`.
- The result, including when the list command fails or prints unparsable
  output, goes through `WithRoutes`.

### 12.8 App

`frontend/src/renderer/components/settings/harness/harness-routes.ts` (new)

- `describeRoute`: "<family> via <host>", prefixed by "<name>: " when named,
  plus " · no key (network access)" for credential `none`.
- `describeRoutes`: one route, or "Providers via <host>: N of M verified"
  (or "Several providers: ..." across hosts).
- `routeStatusLabel`: for `configured` only, and only when no route is
  verified: "Needs network access" (any access_challenge), "Can't reach
  provider" (all unreachable), else "Configured, can't be verified".
- `routesBypassAccounts`: true when every route is a cloud route or carries a
  credential other than `token` / `account` / `unknown`.
- `routeReportLines`: English lines for the copied diagnostics.

`describe-harness.ts`

- Adds `routes`, `routeDescription`. `accountDescription` now appends the
  route line to the account line. `authSummary` uses `routeStatusLabel` for
  configured routes.

`HarnessAccountTab.tsx`

- For harnesses with a Subscriptions panel (Claude Code, Codex), a "Model
  route" group above it, with the bypass note when `routesBypassAccounts`.

`HarnessHealthTab.tsx`

- One "Model route" row per route (description plus verification label);
  "Connection not checked" is no longer shown when routes explain the verdict.

`harness-diagnostics.ts`

- `healthText` includes `routeReportLines`.

`frontend/src/renderer/i18n/*.json` (all nine locales)

- `settings.harness.route.{via,named,noKey,several,severalHosts,unverifiable,needsNetwork,unreachable,accountsNotUsed}`,
  `settings.harness.route.family.*` (8), `settings.harness.route.verification.*` (8),
  `settings.harness.health.modelRoute`. Keys were appended as text, so no
  existing entry changed.

### 12.9 Behavior changes at a glance

| Setup | Before | After |
|---|---|---|
| Claude Code, Bedrock/Vertex/Foundry | configured (or unknown without the CLI), no route shown | configured, route named, `unverifiable`; unknown → configured |
| Claude Code, keyless `ANTHROPIC_BASE_URL` | unknown (`credential_missing`) | authorized / unauthorized from the listing, else configured |
| Claude Code, keyed gateway or first party | unchanged | unchanged (keyed gateway also shows its route) |
| Codex, custom provider, no `env_key` | unknown, retried as an inconclusive failure | authorized / unauthorized from the listing, else configured |
| Codex, custom provider, `env_key` set | configured | checked with the key: authorized / unauthorized, else configured |
| Codex, custom provider, `env_key` unset | unknown | unknown (unchanged) plus `route_credential_missing` |
| Codex, OpenAI login | unchanged | unchanged |
| OpenCode 2, only MCP credentials stored | authorized | unknown, or decided by provider routes |
| OpenCode 1/2, `baseURL` providers | ignored | identified and checked; decide when nothing else does |

### 12.10 Tests

All pass:

- `backend/internal/adapters/agent/modelroute/probe_test.go` (new): list
  URLs; classification (list, Gemini-style list, JSON without a list, JSON
  401, HTML 403, HTML 200, redirect, 404); public-listing rule; no cleartext
  key; caching and `AO_MODEL_ROUTE_PROBE=off`; aggregation.
- `backend/internal/domain/agent_model_route_test.go` (new): `RouteHost`,
  loopback, sanitizing, bound.
- `backend/internal/adapters/agent/claudecode/route_test.go` (new): Bedrock
  from user settings (no request sent); Bedrock with an AWS profile and no
  CLI; keyless gateway listing and zero-trust page; first party records no
  route. `auth_test.go`'s `clearClaudeCredentialEnv` also clears the route
  variables.
- `backend/internal/adapters/agent/codex/codex_route_test.go` (new): keyless
  verified; network challenge; key accepted (anonymous refused); key rejected;
  `env_key` unset.
- `backend/internal/adapters/agent/opencode/route_test.go` (new): JSONC
  config with keyless, Bedrock, `{env:}` keyed and stored providers;
  default-first order; no key values in sources; aggregation.
- `opencodev2_test.go`: MCP-only list → unknown (fails without the fix).
- `backend/internal/service/agent/model_routes_test.go` (new): routes reach
  the observation sanitized; reason names the route; readiness stays unknown.
- `describe-harness.test.ts`: route labels, several providers, no routes,
  bypass rule, report lines.
- Isolation: `opencode/import_prepare_test.go`'s `TestMain` and
  `opencodev2/main_test.go` (new) point `ConfigHome` at an empty temp dir and
  clear `ManagedConfigDirs`.

Checks run:

- golangci-lint v2.14.0: 0 issues.
- `go test -race` on `modelroute` and `opencode`: clean.
- `npm run typecheck`: clean.
- Settings and i18n vitest suites: 766 passed.
- Full `go test ./...`: the 7 packages that fail on clean HEAD on this machine
  fail here too (commandcode, modelcatalog, opencodev2's binary-resolution
  tests, chatdriver/opencodev2acp, reviewer/opencodev2, runtime/tmux,
  shellterm). `internal/process` failed once under load and passed 3/3 alone;
  that package is untouched.

### 12.11 Real-machine check

Isolated lab (scratch `AO_DATA_DIR`, separate port), reading this computer's
own harness configs read-only:

| Harness | Verdict | Route |
|---|---|---|
| Claude Code | configured, "Configured, can't be verified" | AWS Bedrock via the company gateway, no key |
| Codex | authorized, "Connected" (was unknown) | custom provider via the gateway, no key, listing 200 |
| OpenCode 2 | authorized, "Connected" | 4 gateway providers: 3 verified, the Bedrock passthrough inconclusive (404) |

### 12.12 Known limits

- Bedrock, Vertex and Foundry routes stay unverified: AO will not sign
  requests with the user's cloud identity or make a model call.
- A keyless listing proves only that the endpoint answers this machine; a
  gateway that allows listing but refuses chat is caught at session launch.
- OpenCode providers whose key is `{file:...}` or stored in OpenCode's own
  store are identified but not checked.
- An OpenCode environment key for a built-in provider still counts as a login
  on presence alone (unchanged, C1).
- Remote hosts and cloud harnesses are out of scope.
- A keyed Claude Code gateway is still checked by the pre-existing validator
  (C1): it sends the key on its own model-list requests without the
  public-listing rule or the cleartext guard of section 5. Hardening it is a
  separate follow-up (ruling R19).

## 13. Rulings (feat/task-plans build)

Each ruling: what was decided, why, and what it costs if wrong.

R1 (M1). `modelcatalog` calls `jsonc.Strip` directly; there is no `stripJSONC`
    wrapper left. Smallest change with the same behaviour. Cost: none.
R2 (M1). A base URL carrying userinfo (`https://user:pw@host`) is not probed:
    `inconclusive`, nothing sent. The credential would ride in the URL and
    could not be held back for the public-listing comparison in section 5.
    Cost: such a route reads configured instead of authorized.
R3 (M1). `modelroute` also exports `Decide(ctx, existing, routes)` (section 6
    over a harness's existing verdict, recording the verified route as the
    account evidence: method from the credential, source from the variable or
    file name, provider from the route name) and `RecordMissingCredential`.
    Every harness applies section 6 the same way through them. Cost: none.
R4 (M1). The reason text names one route as "<family> via <host>", wrapped as
    "provider <name> (...)" when the route has an id, plus " with no key" for a
    keyless route; several routes read "several providers via <host>" (one
    host) or "several providers". Cost: wording only.
R5 (M1). The check cache key is the listing URL plus a SHA-256 of the sorted
    credential header names and values. `unreachable` results are cached like
    any other (only cancelled checks are not); "Check again" drops the cache.
    Cost: a VPN connected less than 5 minutes ago reads unreachable until
    "Check again".
R6 (M1). `InvalidateAgentAuthentication` drops every cached route check, not
    only that harness's: one gateway can serve several harnesses. Cost: an
    extra listing request per harness after a recheck.
R7 (M1). `AO_MODEL_ROUTE_PROBE` is read on every check and only `off`
    (any case) disables probes. Cost: `0` or `false` do not disable.

R8 (M2). Claude Code's cloud route is taken, in order, from the provider the
    existing ladder resolved, then exactly one truthy `CLAUDE_CODE_USE_*`
    switch in `ClaudeRouteEnv` (environment, else the user's settings.json),
    then a cloud `apiProvider` from `claude auth status`. Otherwise
    `ANTHROPIC_BASE_URL` makes a gateway route. `ResolveClaudeRouteEnv` takes
    the launch context before settings are merged, so `ConfigSource` can tell
    a variable from the settings file. Cost: a project-level cloud switch is
    not shown (project files are never read for routes).
R9 (M2). A keyless Claude gateway whose user settings name an `apiKeyHelper`
    is recorded with credential `unknown` and is not probed: Claude Code sends
    the helper's key, which AO never runs, so a keyless listing would not be
    credential-faithful. Cost: such a setup reads configured, not authorized.
R10 (M2). Launch is unchanged (section 2 non-goals): during
    `ValidateLaunchAuth` a keyless gateway's `rejected` listing does not
    decide the verdict, so it never blocks a spawn. Readiness still shows
    "needs authentication". Cost: a spawn against a gateway that really
    refuses runs until its first request fails.
R11 (M2). `claudecode.InvalidateAuthCache` (called after a runtime 401) also
    drops the route check cache, as section 5 asks. Cost: none.
R12 (M3, R-B). `codex_account_service.go` does exist on this branch, and
    `authenticationWithAccount` already skips custom providers through
    `codexagent.UsesCustomModelProvider()`. It is unchanged, so the adapter's
    route verdict is what readiness uses. Cost: none.
R13 (M3). The Codex check sends every `http_headers` value and every resolved
    `env_http_headers` value, as Codex does, plus `Authorization: Bearer
    <env_key value>`. Any header makes the check keyed (public-listing
    comparison, no cleartext off loopback). A header named `Authorization`,
    or one containing `api-key` or `token`, sets credential `token`.
    `query_params` are not applied. Cost: a provider that needs a query
    parameter on its model list reads inconclusive (configured).
R14 (M3). An unset variable behind an auth header in `env_http_headers` is
    treated like an unset `env_key`: `route_credential_missing`, verdict
    `unknown`. Cost: none.
R15 (M3). OpenCode 1 returns an existing error (unreadable credential store)
    before routes are checked, so the readiness failure is unchanged (C1).
    OpenCode 2's failed or unparsable `auth list` goes through the routes, as
    section 12.7 says. Cost: a broken OpenCode 1 auth.json hides routes until
    it is fixed.
R16 (M3). The codex, opencode and opencodev2 test packages point
    `modelroute.Client` at a transport that refuses everything in `TestMain`,
    so no fixture reaches whatever listens on this machine. The existing Codex
    fixture on 127.0.0.1:4000 now reads `unreachable`, which is still
    `configured`, so the test is unchanged. Cost: none.
R17 (M3). An OpenCode provider with a `baseURL`, no `apiKey`, and an `env`
    list sends the first set variable as its key. None set means keyless.
    Cost: none.
R18 (fix after M2 verification). `ResolveClaudeRouteEnv` uses the resolver's
    precedence: explicit launch environment, then the user settings when they
    define the key (an empty entry shadows), then the process environment.
    `ConfigSource` names the source whose value was used; a gateway URL that
    only the project's settings set reads `.claude/settings.json`. Before
    this, the process environment won for route values while the probe used
    settings, so the route could name the wrong host or source. Cost: none.
R19 (coordinator ruling). The section 5 safety and public-listing rules apply
    to AO's new probe only. A keyed Claude gateway keeps the existing
    validator unchanged under C1, including its keyed requests, and the route
    maps `provider_accepted` to `verified`. Hardening that validator is a
    separate follow-up (section 12.12). Cost: a public model list read
    through a keyed gateway still reads verified.
