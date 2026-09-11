# Anthropic-compatible endpoints behind `ANTHROPIC_BASE_URL` (spike, 2026-09-11)

Phase 0 of `docs/superpowers/plans/2026-09-10-provider-roadmap.md`. Two questions: what do
Anthropic's own documents say about pointing the `claude` CLI at a backend they don't operate,
and does Sirdar's existing Claude adapter path survive it. Probed against Claude Code 2.1.267
and Ollama 0.32.9 on macOS.

Short version: the mechanism works unchanged — `system/init`, tool calls and `structured_output`
all behave — and Anthropic's gateway documentation says in one sentence that they don't support
it. No clause prohibits it either.

## Part 1 — What the primary text says

### Prohibited

**Routing a Claude subscription's credentials through someone else's product.**
[code.claude.com/docs/en/legal-and-compliance](https://code.claude.com/docs/en/legal-and-compliance),
"Authentication and credential use":

> Anthropic does not permit third-party developers to offer Claude.ai login into their own
> applications, or to route requests through Free, Pro, or Max plan credentials on behalf of
> their users. Moreover, developers may not collect, store, or intermediate Claude.ai
> credentials or session tokens — sign-in to a Claude account must complete through Anthropic's
> own flow.

This is about *whose* credential is used, not about which host serves the request. It bites on
the base-URL case only in the specific way described under "the leak" below.

**Building a competing service on the Services.** Commercial Terms D.4:

> Customer may not and must not attempt to (a) access the Services to build a competing product
> or service, including to train competing AI models or resell the Services except as expressly
> approved by Anthropic; (b) reverse engineer or duplicate the Services; or (c) support any
> third party's attempt at any of the conduct restricted in this sentence.

Consumer Terms section 3 carries the parallel restriction:

> To develop any products or services that compete with our Services, including to develop or
> train any artificial intelligence or machine learning algorithms or models or resell the
> Services.

Neither clause reaches a user running the published CLI against their own model endpoint;
"the Services" is Anthropic's inference, which such a session never touches.

### Permitted (documented, with a support disclaimer)

**`ANTHROPIC_BASE_URL` is a documented, supported variable.**
[code.claude.com/docs/en/env-vars](https://code.claude.com/docs/en/env-vars):

> Override the API endpoint to route requests through a proxy or gateway. When set to a
> non-first-party host, MCP tool search is disabled by default. Set `ENABLE_TOOL_SEARCH=true`
> if your proxy forwards `tool_reference` blocks. As of v2.1.196, Remote Control is disabled
> when this points at a host other than `api.anthropic.com`, matching its behavior on Amazon
> Bedrock, Google Cloud's Agent Platform, and Microsoft Foundry

The phrase "non-first-party host" is Anthropic's own, and the documented consequence is
degraded features, not a policy violation.

**Third-party gateways are documented as a first-class deployment, with the endorsement
withheld.** [code.claude.com/docs/en/llm-gateway](https://code.claude.com/docs/en/llm-gateway):

> Any gateway that exposes a supported API format works. Anthropic doesn't endorse, maintain, or
> audit third-party gateway products, and doesn't support routing Claude Code to non-Claude
> models through any gateway.

Read it precisely. "Doesn't support" sits in a sentence about endorsement, maintenance and
auditing — it is a statement about what Anthropic will help with and keep working, not a
prohibition. There is no "must not", no "not permitted", and no enforcement language anywhere
near it, in contrast to the credential clause above, which has all three. This is the closest
thing to a position on non-Claude models behind the CLI, and it is a support disclaimer.

**A gateway credential replaces the subscription, and that is described as ordinary.** Same page,
"Subscriptions and gateways":

> While a gateway credential variable or `apiKeyHelper` is active, a developer's claude.ai
> subscription isn't used: the credential replaces the subscription login for that session, and
> the subscription's usage limits don't apply. That traffic is billed per token to whoever owns
> the credential the gateway forwards, such as your organization's Anthropic Console account, or
> your Amazon Bedrock, Google Cloud's Agent Platform, or Microsoft Foundry account when the
> gateway routes there.

**Provisioning your own keys is explicitly carved out of the credential restriction.**
Legal and compliance, immediately after the prohibition:

> This does not restrict how customers provision and manage their own API keys or third-party
> inference provider credentials — for example, configuring an API key in a development
> environment, secrets manager, or machine image for use by the customer's own authorized users
> — provided the resulting usage is billed to the key owner under their agreement with Anthropic
> (or the applicable provider) and is not resold or intermediated as described above.

**The credential header shapes are documented.**
[code.claude.com/docs/en/llm-gateway-protocol](https://code.claude.com/docs/en/llm-gateway-protocol),
request headers table:

> `Authorization`, `x-api-key` — The developer's gateway credential, in one or both headers
> depending on which credential variable they set

and [env-vars](https://code.claude.com/docs/en/env-vars):

> **ANTHROPIC_AUTH_TOKEN** — Custom value for the `Authorization` header (the value you set here
> will be prefixed with `Bearer `)
>
> **ANTHROPIC_API_KEY** — API key sent as `X-Api-Key` header. When set, this key is used instead
> of your Claude Pro, Max, Team, or Enterprise subscription even if you are logged in. In
> non-interactive mode (`-p`), the key is always used when present.

### Not addressed

**Nothing in the Consumer Terms, the Commercial Terms or the Usage Policy mentions
`ANTHROPIC_BASE_URL`, proxies, claude-code-router, LiteLLM, or running the harness against a
non-Anthropic model.** Fetched 2026-09-11:
[consumer-terms](https://www.anthropic.com/legal/consumer-terms),
[commercial-terms](https://www.anthropic.com/legal/commercial-terms),
[aup](https://www.anthropic.com/legal/aup). The nearest Commercial Terms language is A.2, and it
disclaims rather than restricts:

> Customer may elect (in its sole discretion) to use features, services or other content made
> available by third parties to Customer through the Services ("Third Party Features"). Customer
> acknowledges and agrees that Third Party Features are not Services and, accordingly, Anthropic
> is not responsible for them.

The Usage Policy's only adjacent rules are about model scraping or distillation "without prior
authorization from Anthropic", jailbreaking, and ban evasion. None is engaged by serving your own
model to your own CLI.

**No OpenAI-compatible or generic-proxy endpoint is documented, and none is discouraged by name.**
The gateway compatibility guide lists exactly three API formats a gateway may expose — Anthropic
Messages (`ANTHROPIC_BASE_URL`), Amazon Bedrock InvokeModel, and Google Cloud's Agent Platform
rawPredict — plus Foundry and Claude Platform on AWS, which "implement the Anthropic Messages
format". A Chat Completions endpoint is not among them, and claude-code-router and LiteLLM are
not named anywhere in the docs. An OpenAI-compatible backend therefore needs a translating proxy
in front of it; that proxy is the documented "gateway", and Sirdar's existing `provider: openai`
already covers the same ground without one.

**Whether a *model* served behind `ANTHROPIC_BASE_URL` may be a non-Claude model is addressed
only by the support disclaimer above.** No terms document speaks to it.

### Verdict

| Question | Answer |
|---|---|
| Point the unmodified `claude` CLI at a non-Anthropic host via `ANTHROPIC_BASE_URL` | Documented and supported as a mechanism; not prohibited anywhere |
| Serve a non-Claude model behind it | Explicitly **unsupported** (`llm-gateway`), nowhere prohibited |
| Do it with an API key or gateway token | Permitted; the key-provisioning carve-out covers it directly |
| Do it while signed in with a Pro/Max subscription | The credential clause is about third parties routing *other people's* subscriptions; a user pointing their own CLI somewhere is not that. But see the leak below — do not do it by accident |
| Consumer / Commercial Terms / Usage Policy position | Silent |

### The leak worth knowing about

From [llm-gateway](https://code.claude.com/docs/en/llm-gateway):

> `ANTHROPIC_BASE_URL` is the variable that points Claude Code at the gateway. Setting only that
> variable, without a gateway credential, doesn't replace the subscription. Requests still route
> through the gateway, but a saved claude.ai login remains the active credential, so its usage
> limits and billing apply.

And from [llm-gateway-protocol](https://code.claude.com/docs/en/llm-gateway-protocol):

> When the developer authenticates with a claude.ai login, which is possible when
> `ANTHROPIC_BASE_URL` is set without a gateway credential variable, this header also carries an
> OAuth capability that the upstream requires, and stripping it fails those requests with `401`

So a base URL with no credential sends the user's subscription OAuth material to whatever host
is configured. Sirdar's `billing: subscription` strips `ANTHROPIC_API_KEY` from the child
environment (`internal/provider/claude/claude.go:childEnv`) but passes `ANTHROPIC_BASE_URL`
straight through from `os.Environ()`. A stray export in the operator's shell profile is
therefore enough to point a subscription-billed Sirdar run at a third party. That is the one
configuration that turns a supported mechanism into a credential-handling problem, and it is
the reason for the recommendation in Part 3.

## Part 2 — The spike

### Ollama: present, no models, endpoint is real

```
$ ollama --version
Warning: could not connect to a running Ollama instance
Warning: client version is 0.32.9
$ ollama list      # after `ollama serve`
NAME    ID    SIZE    MODIFIED
$ llama-server --version
command not found
```

Ollama 0.32.9 is installed with **zero models pulled**, and llama.cpp's server is absent. No
model was downloaded for this spike. Ollama does, however, already serve the Anthropic Messages
format — the route exists and answers in Anthropic's error envelope:

```
POST /v1/messages          400  {"type":"error","error":{"type":"invalid_request_error","message":"model is required"},...}
POST /v1/messages          404  {"type":"error","error":{"type":"not_found_error","message":"model 'x' not found"},...}
POST /v1/nonexistent-route 404  404 page not found        <- what a missing route actually looks like
GET  /v1/models            200  {"object":"list","data":null}
POST /api/hello            404  404 page not found        <- the connection-warming probe; harmless
```

Driving the real CLI at it with a model that isn't installed gives a clean, well-shaped failure
rather than a hang:

```
$ ANTHROPIC_BASE_URL=http://localhost:11434 ANTHROPIC_API_KEY=ollama ANTHROPIC_MODEL=qwen3-coder \
  claude -p --output-format stream-json --input-format stream-json --verbose --max-turns 1
```

`system/init` arrives normally with `"model":"qwen3-coder"` and
`"apiKeySource":"ANTHROPIC_API_KEY"`, then:

```json
{"type":"result","subtype":"success","is_error":true,"api_error_status":404,
 "terminal_reason":"api_error","num_turns":1,"total_cost_usd":0,"modelUsage":{},
 "result":"There's an issue with the selected model (qwen3-coder). It may not exist or you may not have access to it. Run --model to pick a different model."}
```

Note `subtype: "success"` alongside `is_error: true` — a Sirdar adapter must key off `is_error`
and `terminal_reason`, not `subtype`.

To exercise it for real, pull one tool-capable model first. Recommended: **`qwen3-coder`**
(strongest tool-calling of the candidates, ~19 GB for the 30B-A3B quant); `qwen2.5-coder:7b`
(~4.7 GB) is the cheap alternative if download size matters more than tool fidelity.

```
ollama serve &
ollama pull qwen3-coder
ANTHROPIC_BASE_URL=http://localhost:11434 ANTHROPIC_API_KEY=ollama ANTHROPIC_MODEL=qwen3-coder \
claude -p --output-format stream-json --input-format stream-json --verbose --max-turns 3 \
  --json-schema '{"type":"object","properties":{"greeting":{"type":"string"},"n":{"type":"integer"}},"required":["greeting","n"],"additionalProperties":false}'
```

### The adapter path, exercised against a controlled backend

Because no local model was available, the probe ran against a logging mock that speaks the
Anthropic Messages format and returns a scripted SSE stream — a `Bash` tool call, then a
`StructuredOutput` call. This answers the adapter questions exactly, and additionally captures
the wire shape a real third-party endpoint has to satisfy. The mock is
`mockanthropic.py` in this session's scratchpad; it is not product code and nothing was added to
the repo.

Command as run, mirroring `docs/research/06-wire-formats.md`:

```
printf '%s\n' '{"type":"user","message":{"role":"user","content":"Run the shell command `echo hi` using the Bash tool, then answer with the JSON object."}}' | \
ANTHROPIC_BASE_URL=http://localhost:8787 ANTHROPIC_API_KEY=sirdar-spike ANTHROPIC_MODEL=mock-model \
claude -p --output-format stream-json --input-format stream-json --verbose --max-turns 3 \
  --json-schema '{"type":"object","properties":{"greeting":{"type":"string"},"n":{"type":"integer"}},"required":["greeting","n"],"additionalProperties":false}'
```

**Everything Sirdar depends on worked.** Exit 0.

- `system/init` arrives with `"model":"mock-model"`, `"apiKeySource":"ANTHROPIC_API_KEY"`,
  `"permissionMode":"default"`, `"claude_code_version":"2.1.267"`, and the full tool and MCP
  lists. The resume handle (`session_id`) is present as usual.
- The `Bash` tool call flowed, the CLI **executed `echo hi` locally**, and returned
  `{"tool_use_id":"toolu_mock1","type":"tool_result","content":"hi","is_error":false}` with the
  usual `tool_use_result` sidecar. Tool execution is entirely client-side, so it is unaffected
  by which backend produced the call.
- Structured output arrived on both paths described in `06-wire-formats.md`: the synthetic
  `StructuredOutput` tool call, and the parsed object on the result line —
  `"structured_output":{"greeting":"hi from mock backend","n":7}`, with `num_turns: 3`,
  `stop_reason: "tool_use"`, `is_error: false`.
- Latency: 3145 ms wall for the whole process, of which `duration_ms` 105 and `duration_api_ms`
  28. Startup dominates; against a real local model the model's own generation time is added on
  top, unchanged by the base-URL indirection.

### What the CLI actually sends, and what will break on a real backend

Request line: `POST /v1/messages?beta=true`. Headers, captured verbatim:

```
x-api-key: sirdar-spike
anthropic-version: 2023-06-01
anthropic-beta: claude-code-20250219,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,
                context-management-2025-06-27,prompt-caching-scope-2026-01-05,
                mid-conversation-system-2026-04-07,mid-conversation-tool-changes-2026-07-01,
                advisor-tool-2026-03-01,effort-2025-11-24
user-agent: claude-cli/2.1.267 (external, sdk-cli)
x-app: cli
x-claude-code-session-id: <session uuid>
x-stainless-*: (SDK telemetry)
```

Swapping the credential variable changes only the header, as documented — with
`ANTHROPIC_AUTH_TOKEN=sk-gateway-tok` the request carries `Authorization: Bearer sk-gateway-tok`
and no `x-api-key`. No `anthropic-beta` value is dropped in either case.

Body keys on every turn:

```
["context_management","max_tokens","messages","metadata","model","output_config","stream","system","thinking","tools"]
```

with `"thinking": {"type":"adaptive"}`. Three of those are the known breakage points, and the
gateway compatibility guide names each:

- **`thinking: {"type":"adaptive"}`** is sent unconditionally here. The guide says Claude Code
  "treats model names it doesn't recognize, such as gateway aliases, as current models that
  receive the field", and the symptom is a `400` naming `thinking` or `adaptive`. It also says
  Claude Code retries and disables the capability for the rest of the conversation when the
  upstream rejects `thinking`, so a well-behaved backend that returns a clear `400` recovers;
  one that 500s or hangs does not.
- **`output_config`** carries the structured-output settings that make `--json-schema` work.
  A backend that rejects unknown body fields returns `400 Extra inputs are not permitted` and
  Sirdar loses `structured_output` — and the guide is explicit that Claude Code does **not**
  retry that one.
- **`context_management`**, same failure mode, also not retried.
  `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` turns off context management and the beta tool
  fields, but not adaptive thinking, which is selected by model name.

Two more findings that matter for Sirdar specifically:

- **Model name mapping is the caller's problem.** An unrecognized model produces a non-fatal
  stderr line, `[claude-code:unrecognized_model] {"model":"mock-model","query_source":"sdk"}`,
  and the run proceeds. Providers handle this differently: DeepSeek maps `claude-opus*` to
  `deepseek-v4-pro` and `claude-sonnet*`/`claude-haiku*` to `deepseek-flash` server-side, while
  Z.ai expects you to pin `ANTHROPIC_DEFAULT_*_MODEL` client-side.
- **Reported cost is fabricated.** The result line carried
  `"total_cost_usd":0.00136` with `modelUsage.mock-model` showing `"provider":"firstParty"`,
  `"costBasis":"unknown"`, `"contextWindow":200000` and `"maxOutputTokens":32000` — all
  first-party defaults applied to a model the CLI has never heard of. `budget.maxUsd` is
  therefore meaningless behind a foreign base URL, and Sirdar should not present those dollars
  as real. Turn and minute budgets remain sound.
- Request body on the first turn was **230,467 bytes** on this machine (system prompt plus every
  loaded MCP tool schema). A local model with a small context window will reject that outright,
  which is an argument for `mcp.workspaceOnly` staying on for these runs.

### Third-party endpoints that publish this path

Every one of these answered an unauthenticated `POST .../v1/messages` with a `401`, confirming
the route exists (probed 2026-09-11):

| Provider | `ANTHROPIC_BASE_URL` | Credential variable | Model naming |
|---|---|---|---|
| OpenRouter | `https://openrouter.ai/api` | `ANTHROPIC_AUTH_TOKEN`, with `ANTHROPIC_API_KEY=""` | `anthropic/claude-sonnet-4`, `~anthropic/claude-opus-latest[1m]` |
| DeepSeek | `https://api.deepseek.com/anthropic` | `ANTHROPIC_API_KEY` | server-side mapping from `claude-*` |
| Z.ai (Zhipu GLM) | `https://api.z.ai/api/anthropic` | `ANTHROPIC_AUTH_TOKEN` | `ANTHROPIC_DEFAULT_*_MODEL` = `GLM-4.7` / `GLM-4.5-Air` |
| MiniMax | `https://api.minimax.io/anthropic` | `X-Api-Key`, i.e. `ANTHROPIC_API_KEY` | not published on a Claude Code page |
| Moonshot / Kimi | `https://api.moonshot.ai/anthropic` | `ANTHROPIC_API_KEY` (endpoint live) | `kimi-k3`, `kimi-k2.7-code-highspeed` |
| Ollama (local) | `http://localhost:11434` | any non-empty `ANTHROPIC_API_KEY` | the Ollama model tag |

OpenRouter is the only one publishing a full Claude Code page
([cookbook/coding-agents/claude-code-integration](https://openrouter.ai/docs/cookbook/coding-agents/claude-code-integration)):

```bash
export OPENROUTER_API_KEY="<your-openrouter-api-key>"
export ANTHROPIC_BASE_URL="https://openrouter.ai/api"
export ANTHROPIC_AUTH_TOKEN="$OPENROUTER_API_KEY"
export ANTHROPIC_API_KEY="" # Important: Must be explicitly empty
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1 # Optional
```

with model classes pinned separately (`ANTHROPIC_DEFAULT_SONNET_MODEL`, `..._OPUS_MODEL`,
`..._HAIKU_MODEL`, `..._FABLE_MODEL`, `CLAUDE_CODE_SUBAGENT_MODEL`). Their caveats: `.env` files
are not read by the native installer, `/logout` first if a claude.ai login is cached, and `/fast`
needs a pinned Opus id rather than a `latest` alias. Note that their `ANTHROPIC_API_KEY=""` is
the same defensive move Sirdar already makes by stripping the variable.

Two documented requirements a self-hosted backend must meet, both from the compatibility guide:
stream rather than buffer ("if your gateway buffers complete responses before relaying them,
Claude Code stalls"), and emit SSE `ping` events during silent gaps, because Claude Code
"aborts a stream that goes silent for 300 seconds by default" on `ANTHROPIC_BASE_URL`
connections.

### Status

Spike **exercised** against a controlled Anthropic-format backend: the full Sirdar Claude adapter
path works. **Not exercised** against a real local model — Ollama has no models installed and
pulling one was out of scope. The remaining unknown is behavioural, not structural: whether a
small open model drives the Claude harness's tool loop well enough to finish a triage run.

## Part 3 — Recommendation

### Do not add a `claude.baseUrl` config key

Leave it to the environment. The reasoning:

`ANTHROPIC_BASE_URL` already reaches the child process — `childEnv` copies `os.Environ()` and
filters exactly one variable — so a user who exports it gets the behaviour today with no code.
A config key would buy nothing except a second place for the value to live.

It would also buy a real hazard. The key would have to be inert under `billing: subscription`,
because that combination is precisely the one that ships the user's claude.ai OAuth material to
a third-party host, and a config key that silently does nothing in the default billing mode is
worse documentation than no key at all. Anthropic's own framing supports the environment route:
the variable is what an administrator distributes through managed settings, and Claude Code
reads `.claude/settings.json`'s `env` block on its own. A user who wants this can set it there
and Sirdar never has to know.

What Sirdar should do instead is defend the default. Add one check to `sirdar doctor` and to the
run preamble: **if `ANTHROPIC_BASE_URL` is set to a host other than `api.anthropic.com` while
`billing: subscription` is in effect, refuse the run** and say why. That converts an accidental
credential leak into an error message, costs a few lines, and needs no new configuration surface.
If a user genuinely wants a third-party endpoint, `billing: api` plus an exported base URL and
credential is the supported combination, and the refusal message should say so.

Revisit only if a second signal appears — someone actually running triage this way and wanting
it per-workspace rather than per-shell.

### Draft section for README / docs

> ### Anthropic-compatible endpoints
>
> Sirdar spawns your own `claude` binary, so anything you can configure Claude Code to do, a
> Sirdar run inherits. That includes pointing it at an Anthropic-compatible endpoint you control
> with `ANTHROPIC_BASE_URL` — a local Ollama server, or a vendor that publishes an
> Anthropic-format route such as OpenRouter, DeepSeek, Z.ai or MiniMax.
>
> Sirdar has no config key for this, and won't be adding one. Export the variables, or put them
> in `.claude/settings.json`'s `env` block, and set `billing: api` so the endpoint's credential
> is the one in play:
>
> ```bash
> export ANTHROPIC_BASE_URL=http://localhost:11434
> export ANTHROPIC_API_KEY=ollama
> export ANTHROPIC_MODEL=qwen3-coder
> ```
>
> ```yaml
> billing: api
> ```
>
> **`billing: subscription` plus a third-party base URL is refused.** With no credential
> variable set, Claude Code keeps your claude.ai login as the active credential and sends its
> OAuth material to whatever host the base URL names. Sirdar stops the run rather than let that
> happen by accident.
>
> **What works.** The full adapter path — the `system/init` handshake, tool calls, local tool
> execution, permission control requests, and `structured_output` on the result line. Verified
> against an Anthropic-format backend on Claude Code 2.1.267.
>
> **What doesn't.** Reported cost is invented: Claude Code prices unknown models at first-party
> rates, so `budget.maxUsd` is meaningless here and `state.json`'s `costUsd` should be ignored.
> Use `budget.maxTurns` and `budget.maxMinutes`, which are unaffected. `--resume` across a
> backend change is untested. Endpoints that reject unknown request fields break
> `--json-schema` with `400 Extra inputs are not permitted` on `output_config`, and Claude Code
> does not retry that; `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` helps with
> `context_management` but not with adaptive thinking. Keep `mcp.workspaceOnly: true` — a first
> turn carrying every MCP tool schema ran to 230 KB here, which a small local context window
> will refuse.
>
> **Where Anthropic stands.** The mechanism is documented and supported; serving a non-Claude
> model through it is not. From
> [Other LLM gateways](https://code.claude.com/docs/en/llm-gateway): "Any gateway that exposes a
> supported API format works. Anthropic doesn't endorse, maintain, or audit third-party gateway
> products, and doesn't support routing Claude Code to non-Claude models through any gateway."
> That is a support disclaimer, not a prohibition — no Anthropic terms document addresses the
> case. What *is* prohibited is third parties routing other people's subscriptions: "Anthropic
> does not permit third-party developers to offer Claude.ai login into their own applications,
> or to route requests through Free, Pro, or Max plan credentials on behalf of their users"
> ([Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)). Sirdar never
> touches your credentials — the CLI owns its own keychain entry, and the only thing Sirdar does
> to the child environment is remove `ANTHROPIC_API_KEY` when you ask for subscription billing.
>
> If your endpoint speaks OpenAI Chat Completions rather than the Anthropic Messages format, use
> `provider: openai` instead. It talks to that shape directly and needs no translating proxy.

### Also worth folding in

`docs/config.md` should note that `budget.maxUsd` is unreliable behind a non-Anthropic base URL,
alongside the existing note that it is an end-of-run check with Claude.

## Sources

code.claude.com/docs/en/legal-and-compliance · code.claude.com/docs/en/llm-gateway ·
code.claude.com/docs/en/llm-gateway-connect · code.claude.com/docs/en/llm-gateway-protocol ·
code.claude.com/docs/en/env-vars · code.claude.com/docs/en/third-party-integrations ·
anthropic.com/legal/consumer-terms · anthropic.com/legal/commercial-terms · anthropic.com/legal/aup ·
openrouter.ai/docs/cookbook/coding-agents/claude-code-integration ·
openrouter.ai/docs/api/api-reference/anthropic-messages/create-a-message ·
api-docs.deepseek.com/guides/anthropic_api · docs.z.ai/scenario-example/develop-tools/claude ·
platform.kimi.ai/docs/guide/agent-support · live endpoint probes 2026-09-11
