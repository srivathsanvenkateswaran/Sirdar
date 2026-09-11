# Bring-your-own-subscription: what is allowed (checked 2026-09-10)

## Verdicts

**Anthropic (Claude Pro/Max).** Spawning the *unmodified* official `claude` binary and letting
the user sign in through Anthropic's own `/login` flow is permitted. Using the OAuth token
directly against the API is prohibited and server-enforced. Using the Agent SDK with a
subscription login is "not allowed unless previously approved" in the docs, yet Anthropic's own
Help Center currently meters it as subscription usage. Design for the CLI-spawn path.

**OpenAI (ChatGPT Plus/Pro via Codex).** No written prohibition on third-party tools driving
`codex` / `codex app-server` / the Codex SDK with a ChatGPT login. OpenAI staff have declined to
give a clear yes. The ecosystem (OpenClaw, Conductor, Paperclip, T3, Vibe Kanban) does it openly.

## Anthropic: current text

Legal and compliance page, section "Authentication and credential use"
(code.claude.com/docs/en/legal-and-compliance):

> OAuth authentication is intended exclusively for purchasers of Claude Free, Pro, Max, Team, and
> Enterprise subscription plans and is designed to support ordinary use of Claude Code and other
> native Anthropic applications. Developers building products or services that interact with
> Claude's capabilities, including those using the Agent SDK, should use API key authentication
> through Claude Console or a supported cloud provider. Anthropic does not permit third-party
> developers to offer Claude.ai login into their own applications, or to route requests through
> Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not
> collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude
> account must complete through Anthropic's own flow. [...] Nor does it prevent an end user from
> signing in to the unmodified Claude Code binary with their own Claude subscription, including
> where a platform hosts Claude Code as described under "Can customers offer Claude Code in your
> products?" above.

"Can customers offer Claude Code in their products?" on the same page: the binary must not be
modified and no built-in auth method may be removed or restricted; you may not pay for, resell
or intermediate usage; each end user authenticates with their own credentials and is billed by
Anthropic. You may say in plain text that your product "runs Claude Code" but cannot use the
name or logo in branding. "Advertised usage limits for Pro and Max plans assume ordinary,
individual usage of Claude Code and the Agent SDK."

The wording softened since February 2026. The previous text was "Using OAuth tokens obtained
through Claude Free, Pro, or Max accounts in any other product, tool, or service — including the
Agent SDK — is not permitted" (The Register 2026-02-20). That sentence is gone; the "unmodified
binary" carve-out is new.

Agent SDK overview and quickstart Note:

> Unless previously approved, Anthropic does not allow third party developers to offer claude.ai
> login or rate limits for their products, including agents built on the Claude Agent SDK. Use the
> API key authentication methods described in the Quickstart instead.

Help Center "Use the Claude Agent SDK with your Claude plan"
(support.claude.com/en/articles/15036540): on 2026-05-14 Anthropic announced that from June 15
programmatic usage (Agent SDK, `claude -p`, GitHub Actions, third-party apps authenticating with
the subscription) would move to a dollar credit ($20 Pro / $100 Max 5x / $200 Max 20x, API
rates, no rollover). On June 15 it was paused: "For now, nothing has changed: Claude Agent SDK,
`claude -p`, and third-party app usage still draw from your subscription's usage limits." The
split is paused, not cancelled. Design for the credit-pool scenario returning.

Enforcement history: 9 Jan 2026 server-side rejection of subscription OAuth tokens from
non-Claude-Code clients (broke OpenCode/Roo/Cline); 19 to 20 Feb legal page update; 4 Apr billing
enforcement. Anthropic engineer Thariq Shihipar, Jan 2026: "Third-party harnesses using Claude
subscriptions create problems for users and are prohibited by our Terms of Service."

Authentication facts that matter for a wrapper (code.claude.com/docs/en/authentication):

- Precedence: Bedrock/Vertex/Foundry > `ANTHROPIC_AUTH_TOKEN` > `ANTHROPIC_API_KEY` >
  `apiKeyHelper` > `CLAUDE_CODE_OAUTH_TOKEN` > profile > subscription `/login`. In `-p` mode an
  API key in the environment always wins. Vibe Kanban strips `ANTHROPIC_API_KEY` from the child
  env for this reason.
- `claude setup-token` mints a one-year subscription OAuth token for CI. A wrapper storing it
  touches the "may not collect, store, or intermediate credentials" clause. Do not store it; let
  the CLI own its keychain entry.
- `--bare` never reads OAuth credentials. A subscription-backed wrapper must not use `--bare`,
  even though the headless doc says it will become the default for `-p` in a future release.
  Pin behaviour explicitly.

## Third-party backends for Claude Code (primary text)

Checked 2026-09-11. Full working, probes and the Sirdar recommendation are in
`docs/research/providers/spike-anthropic-compatible.md`; this section records the quotes.

**The mechanism is documented and supported.** `ANTHROPIC_BASE_URL`
(code.claude.com/docs/en/env-vars):

> Override the API endpoint to route requests through a proxy or gateway. When set to a
> non-first-party host, MCP tool search is disabled by default. Set `ENABLE_TOOL_SEARCH=true` if
> your proxy forwards `tool_reference` blocks. As of v2.1.196, Remote Control is disabled when
> this points at a host other than `api.anthropic.com`, matching its behavior on Amazon Bedrock,
> Google Cloud's Agent Platform, and Microsoft Foundry

`ANTHROPIC_AUTH_TOKEN` on the same page:

> Custom value for the `Authorization` header (the value you set here will be prefixed with
> `Bearer `)

**Non-Claude models behind it are unsupported, not prohibited.**
code.claude.com/docs/en/llm-gateway:

> Any gateway that exposes a supported API format works. Anthropic doesn't endorse, maintain, or
> audit third-party gateway products, and doesn't support routing Claude Code to non-Claude
> models through any gateway.

That sentence is the whole of Anthropic's position on the question. It is framed as endorsement,
maintenance and auditing — no "must not", no "not permitted", no enforcement language, in
contrast to the credential clause quoted above, which carries all three plus "Anthropic reserves
the right to take measures to enforce these restrictions".

**A gateway credential replacing the subscription is described as ordinary.** Same page:

> While a gateway credential variable or `apiKeyHelper` is active, a developer's claude.ai
> subscription isn't used: the credential replaces the subscription login for that session, and
> the subscription's usage limits don't apply. That traffic is billed per token to whoever owns
> the credential the gateway forwards [...]

**Base URL without a credential keeps the subscription active — and ships its OAuth material
to the configured host.** Same page:

> `ANTHROPIC_BASE_URL` is the variable that points Claude Code at the gateway. Setting only that
> variable, without a gateway credential, doesn't replace the subscription. Requests still route
> through the gateway, but a saved claude.ai login remains the active credential, so its usage
> limits and billing apply.

and code.claude.com/docs/en/llm-gateway-protocol, on `anthropic-beta`:

> When the developer authenticates with a claude.ai login, which is possible when
> `ANTHROPIC_BASE_URL` is set without a gateway credential variable, this header also carries an
> OAuth capability that the upstream requires, and stripping it fails those requests with `401`

This is the combination Sirdar must refuse: `billing: subscription` strips `ANTHROPIC_API_KEY`
but passes `ANTHROPIC_BASE_URL` through, so a stray export would send the user's subscription
credential to a third party. `billing: api` plus an explicit credential is the supported shape.

**Provisioning your own keys is carved out of the credential prohibition.**
code.claude.com/docs/en/legal-and-compliance, immediately after the "does not permit third-party
developers" sentence already quoted above:

> This does not restrict how customers provision and manage their own API keys or third-party
> inference provider credentials — for example, configuring an API key in a development
> environment, secrets manager, or machine image for use by the customer's own authorized users
> — provided the resulting usage is billed to the key owner under their agreement with Anthropic
> (or the applicable provider) and is not resold or intermediated as described above.

**Not addressed anywhere in the terms.** The Consumer Terms, the Commercial Terms and the Usage
Policy say nothing about `ANTHROPIC_BASE_URL`, proxies, claude-code-router, LiteLLM, or running
the harness against a non-Anthropic model (all three fetched 2026-09-11). The nearest Commercial
Terms language, A.2, disclaims rather than restricts:

> Customer may elect (in its sole discretion) to use features, services or other content made
> available by third parties to Customer through the Services ("Third Party Features"). Customer
> acknowledges and agrees that Third Party Features are not Services and, accordingly, Anthropic
> is not responsible for them.

The restriction clauses that do exist are aimed elsewhere: Commercial Terms D.4 ("access the
Services to build a competing product or service, including to train competing AI models or
resell the Services"), Consumer Terms section 3 ("To develop any products or services that
compete with our Services"), and the Usage Policy's bar on "Utilization of inputs and outputs to
train an AI model (e.g., 'model scraping' or 'model distillation') without prior authorization
from Anthropic". A session that never reaches Anthropic's inference engages none of them.

**No OpenAI-compatible or generic proxy is documented.** The gateway compatibility guide lists
three API formats a gateway may expose — Anthropic Messages (`ANTHROPIC_BASE_URL`), Amazon
Bedrock InvokeModel (`ANTHROPIC_BEDROCK_BASE_URL`), and Google Cloud's Agent Platform rawPredict
(`ANTHROPIC_VERTEX_BASE_URL`) — with Microsoft Foundry and Claude Platform on AWS implementing
the Anthropic Messages format under their own variables. Chat Completions is not among them, and
claude-code-router and LiteLLM are named nowhere in the docs. Neither is discouraged by name
either. An OpenAI-shaped backend needs a translating proxy, which is the ground Sirdar's
`provider: openai` already covers without one.

**Verdict.** Permitted: pointing the unmodified CLI at a non-Anthropic host, with an API key or
gateway token. Prohibited: third parties routing other people's Free/Pro/Max credentials, and
intermediating claude.ai credentials or session tokens. Not addressed: whether the model behind
the base URL may be a non-Claude model — covered only by the support disclaimer, which carries
no prohibition.

## How the existing wrappers integrate

| Tool | Claude mechanism | Codex mechanism | Inherits login? |
|---|---|---|---|
| T3 Code | `@anthropic-ai/claude-agent-sdk` (`apps/server/src/provider/Layers/ClaudeAdapter.ts`: `query({ pathToClaudeCodeExecutable, settingSources, permissionMode, resume, canUseTool, ... })`); reads SDK `get_usage` and `rate_limit_event` | `codex app-server` via workspace package `effect-codex-app-server` | Yes (Max OAuth from `~/.claude`) |
| Vibe Kanban | Spawns `npx -y @anthropic-ai/claude-code@<pinned>` with `--permission-prompt-tool=stdio --permission-mode <m> --verbose --output-format=stream-json --input-format=stream-json --include-partial-messages --replay-user-messages [--resume <id>]`; prompt over stdin; strips `ANTHROPIC_API_KEY` | `npx -y @openai/codex@<pinned> app-server`, JSON-RPC over stdio, `thread/start` / `thread/fork` | Yes |
| Conductor (closed) | Bundled or system Claude Code binary; warns that `ANTHROPIC_API_KEY` flips to API billing | `codex login` or `CODEX_API_KEY` | Yes |
| Multica (`server/pkg/agent/claude.go`) | `claude -p --output-format stream-json --input-format stream-json --verbose --permission-mode bypassPermissions [--strict-mcp-config] [--mcp-config <path>] [--model] [--max-turns] [--resume <id>]` | `codex app-server --listen stdio://`; config written to `$CODEX_HOME/config.toml` | Yes |
| Paperclip | `claude --print --output-format stream-json --verbose --resume --dangerously-skip-permissions --max-turns --mcp-config`, or ACP; auth priority `ANTHROPIC_API_KEY` > `CLAUDE_CODE_OAUTH_TOKEN` > `~/.claude` login | `codex exec --json` or ACP; host `auth.json` symlinked into a managed `CODEX_HOME` | Yes |
| Claude Squad | tmux running the interactive `claude`; scrapes the pane for prompts | Same with `codex` | Yes |

Pattern: every open-source wrapper except T3 spawns the official binary; T3 and Zed use the SDK.
Nobody compliant touches the OAuth token directly.

## OpenAI policy

- Codex auth doc: CLI and IDE extension support ChatGPT sign-in and API key for local work;
  Codex cloud requires ChatGPT. Headless: device-code auth (beta) or copy `~/.codex/auth.json`
  ("treat it like a password").
- Codex TypeScript SDK bundles the CLI, injects `CODEX_API_KEY`, offers `resumeThread()` and
  `runStreamed()`. App-server: "Embed Codex into your product with the app-server protocol",
  stdio / ws / unix transports. No third-party policy statement.
- openai/codex discussion #8338: staff said the CLI is Apache-licensed and terms are "quite
  permissive", pointed to the Terms of Use, declined to confirm the BYO-ChatGPT-in-a-third-party
  case. OpenClaw's docs claim explicit support; not verified against an OpenAI primary source.
- Plan usage: rolling 5-hour windows plus weekly limits; local and cloud share the allowance.

## Practical constraints (Anthropic)

- Two windows, 5-hour and weekly, shared across claude.ai, Claude Code, Desktop, web, routines.
- Fan-out counts against the same pool. `/usage` attributes plan usage to subagents and MCP
  servers; "each session uses your subscription quota independently"; agent teams roughly 7x
  tokens. Default cap 20 concurrent subagents, nesting depth 3.
- Headless is documented, not discouraged: `-p` starts in manual permission mode, so pass
  `--permission-mode`, `--allowedTools`, `--permission-prompt-tool`, or `--permission-prompts none`
  (v2.1.259+); `--forward-subagent-text` to reconstruct subagent transcripts; SIGINT ends a turn
  cleanly, SIGTERM leaves it unfinished; `-p` waits up to 10 min for background subagents.
- Unattended sessions stop when the `/login` credential expires; warning 3 days out.

## Official primitives a wrapper can rely on

- `claude -p` with `--output-format stream-json --input-format stream-json --verbose
  --include-partial-messages`, `--resume/--session-id/--fork-session`,
  `--mcp-config/--strict-mcp-config`, `--agents <json>`, `--json-schema`, `system/init`
  capabilities (code.claude.com/docs/en/headless).
- Agent SDK (TS/Python): hooks, subagents, MCP, `canUseTool`, sessions, skills, plugins,
  streaming, `get_usage` (code.claude.com/docs/en/agent-sdk/overview).
- Background sessions and supervisor: `claude --bg`, `claude agents --json`, `claude attach
  <id>`, `claude daemon status|stop` (docs/en/agent-view); cross-session messaging.
- Remote Control (subscription only): `claude remote-control`, `--rc`.
- Channels (research preview): Telegram, Discord, iMessage; webhook receiver and permission relay
  in the reference; works under `-p`.
- Routines (research preview): schedule, API (`/v1/claude_code/routines/<id>/fire`), GitHub
  triggers; subscription-metered with a daily run cap.
- Claude Code on the web: `claude --cloud "<task>"`, `--teleport`.
- Codex: `codex app-server --listen stdio://|ws://|unix://` (`thread/start`, `thread/resume`,
  `thread/fork`, `account/rateLimits/read`), `codex exec --json`, `@openai/codex-sdk`.

## Could not verify

- OpenAI help-center articles behind a JS wall; relied on learn.chatgpt.com docs.
- Any OpenAI first-party statement explicitly permitting third-party harnesses on ChatGPT login.
- Conductor's internal mechanism.
- Whether the "unmodified binary" carve-out extends to the binary bundled inside the Agent SDK
  package. Docs are silent. This is the single most important open question for Sirdar's
  provider layer; the safe default is spawning the CLI the user installed and logged into.

## Sources

code.claude.com/docs/en/legal-and-compliance · code.claude.com/docs/en/agent-sdk/overview ·
code.claude.com/docs/en/agent-sdk/quickstart · support.claude.com/en/articles/15036540 ·
support.claude.com/en/articles/11145838 · code.claude.com/docs/en/authentication ·
code.claude.com/docs/en/headless · code.claude.com/docs/en/costs · code.claude.com/docs/en/sub-agents ·
code.claude.com/docs/en/agent-view · code.claude.com/docs/en/remote-control ·
code.claude.com/docs/en/channels · code.claude.com/docs/en/routines ·
code.claude.com/docs/en/claude-code-on-the-web · anthropic.com/legal/consumer-terms ·
theregister.com/software/2026/02/20/anthropic-clarifies-ban-on-third-party-tool-access-to-claude ·
zed.dev/blog/anthropic-subscription-changes · github.com/AndyMik90/Aperant/issues/1871 ·
github.com/pingdotgg/t3code · github.com/BloopAI/vibe-kanban (crates/executors/src/executors/claude.rs, codex.rs) ·
conductor.build/docs/reference/harnesses/claude-code · github.com/multica-ai/multica (server/pkg/agent/claude.go, issues/2563) ·
github.com/paperclipai/paperclip (docs/adapters/claude-local.md) · github.com/smtg-ai/claude-squad ·
learn.chatgpt.com/docs/auth · learn.chatgpt.com/docs/pricing · github.com/openai/codex (sdk/typescript/README.md, discussions/8338) ·
developers.openai.com/codex/app-server · developers.openai.com/community/codex-for-oss · docs.openclaw.ai/providers/openai ·
code.claude.com/docs/en/llm-gateway · code.claude.com/docs/en/llm-gateway-connect ·
code.claude.com/docs/en/llm-gateway-protocol · code.claude.com/docs/en/env-vars ·
code.claude.com/docs/en/third-party-integrations · anthropic.com/legal/commercial-terms ·
anthropic.com/legal/aup
