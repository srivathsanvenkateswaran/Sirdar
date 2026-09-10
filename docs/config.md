# Configuration reference

`sirdar init` writes `.sirdar/config.yaml` for the current workspace with placeholders and
comments; every other command loads it by walking up from the working directory to the nearest
directory that has it. Unknown keys are an error, so a typo in the file fails at load time
rather than being silently ignored.

## Keys

| Key | Type | Default | Meaning |
|---|---|---|---|
| `workspace` | string | directory name (set by `init`) | A label for the workspace; not otherwise interpreted |
| `provider` | string | `claude` | Which agent CLI drives runs: `claude` or `codex` |
| `model` | string | `""` (provider default) | Model name passed to the provider; empty uses the provider's own default |
| `billing` | string | `subscription` | `subscription` strips `ANTHROPIC_API_KEY` from the agent's environment so it uses your CLI login; `api` leaves it in place so usage is billed to the key |
| `sources.tracker` | object, optional | unset | The tracker adapter; see Sources below |
| `sources.helpdesk` | object, optional | unset | The helpdesk adapter; see Sources below |
| `sources.*.adapter` | string | none (required) | `exec` (external adapter process), `zohodesk`/`zendesk`/`freshdesk` (built in), or, for `sources.tracker`, one of `jira`, `linear`, `azdo`, `rally` (built in) |
| `sources.*.command` | string | none (required for `exec`) | Path to the adapter executable |
| `sources.*.orgId` | string | none (required for `zohodesk`) | Zoho Desk organisation id |
| `sources.*.baseUrl` | string | none (required for `zohodesk`); optional override for `zendesk`; `https://rally1.rallydev.com` (default for `rally`) | Zoho Desk API base URL, an override for Zendesk's `https://{subdomain}.zendesk.com`, or the Rally subscription host |
| `sources.*.token` | string | none (one of `token`/`auth` required for `zohodesk`) | Credential reference to a Zoho Desk access token (`env:NAME` or `keychain:SERVICE`) |
| `sources.*.auth` | object | none (one of `token`/`auth` required for `zohodesk`) | OAuth refresh-token grant; see Zoho Desk OAuth below |
| `sources.*.auth.clientId` | string | none (required with `auth`) | Credential reference to the Self Client's client id |
| `sources.*.auth.clientSecret` | string | none (required with `auth`) | Credential reference to the Self Client's client secret |
| `sources.*.auth.refreshToken` | string | none (required with `auth`) | Credential reference to the Self Client's refresh token |
| `sources.*.auth.accountsUrl` | string | derived from `baseUrl` | Zoho accounts server that issues access tokens |
| `sources.helpdesk.subdomain` | string | none (required for `zendesk`) | Zendesk account identifier, e.g. `acme` for `acme.zendesk.com` |
| `sources.helpdesk.oauthToken` | string | none (`zendesk` only; alternative to `email`+`apiToken`) | Credential reference to a Zendesk OAuth bearer token |
| `sources.helpdesk.domain` | string | none (required for `freshdesk`) | Freshdesk account host, e.g. `acme.freshdesk.com` |
| `sources.tracker.baseUrl` | string | none (required for `jira`) | Jira site URL (Cloud) or Data Center instance URL |
| `sources.tracker.deployment` | string | `auto` | `jira` only: `cloud`, `datacenter`, or `auto` (probes `/rest/api/2/serverInfo`) |
| `sources.tracker.email` | string | none (required for `jira` Cloud; required with `apiToken` for `zendesk` basic auth) | The Jira Cloud or Zendesk account email sent with `apiToken` as basic auth; a plain address, not a credential reference |
| `sources.tracker.apiToken` | string | none (required for `jira` Cloud; required with `email`, or use `oauthToken`, for `zendesk`) | Credential reference to a Jira Cloud API token, or a Zendesk API token |
| `sources.tracker.pat` | string | none (required for `jira` Data Center or `azdo`) | Credential reference to a Jira Data Center PAT or an Azure DevOps PAT |
| `sources.tracker.projectKey` | string | unset | `jira` only: scopes `List` to one project |
| `sources.tracker.epicLinkField` | string | unset (resolved by name via `/rest/api/2/field`) | `jira` only: Data Center epic-link custom field id or name |
| `sources.tracker.apiKey` | string | none (required for `linear`, `rally`; required for `freshdesk`) | Credential reference to a Linear personal API key, a Rally API key, or a Freshdesk API key |
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
| `budget.maxTurns` | int | `60` | Agent turns before a run is marked `over_budget` |
| `budget.maxMinutes` | int | `25` | Wall-clock minutes before a run is cancelled and marked `over_budget` |
| `budget.maxUsd` | float | `5` | Cost, from provider usage events, before a run is marked `over_budget` |
| `concurrency` | int | `1` | Parallel runs across the keys passed to `sirdar triage`; overridable with `--concurrency` |
| `permissions.bash` | list of string | `[]` | Glob patterns the agent's `Bash` tool calls must match to be allowed; see Bash permission globs below |
| `playbooks` | string | `.sirdar/playbooks` | Directory of playbook markdown files loaded into the prompt, in filename order |
| `providers.claude.path` | string | `""` (look up `claude` on `PATH`) | Path to the Claude Code binary |
| `providers.codex.path` | string | `""` (look up `codex` on `PATH`) | Path to the Codex binary |

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

`zohodesk`, `zendesk` and `freshdesk` are compiled into Sirdar; only `zohodesk` needs a separate
"Zoho Desk OAuth" section below because of its refresh-token grant. Config load checks what
`zendesk` and `freshdesk` cannot work without:

| Adapter | Required | Notes |
|---|---|---|
| `zendesk` | `subdomain`, and either `email` + `apiToken` or `oauthToken` | `baseUrl` optionally overrides `https://{subdomain}.zendesk.com` |
| `freshdesk` | `domain`, `apiKey` | `domain` is the full account host, e.g. `acme.freshdesk.com` |

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

`sirdar doctor` prints one row per built-in helpdesk, from a single authenticated call — fetching
the signed-in user for `zendesk`, the signed-in agent for `freshdesk` — and names who the
connection authenticates as, never the credential itself:

```
[OK] sources.helpdesk (zendesk) — reachable as you@acme.com
[OK] sources.helpdesk (freshdesk) — reachable as acme.freshdesk.com
```

A Zendesk source authenticated with `oauthToken` instead of `email`/`apiToken` reports `reachable
as oauth`, since there is no account email to show for that grant.

## Credential references

`sources.*.token` (and any credential in config) is never a literal secret: config load
rejects a value that doesn't start with `env:` or `keychain:`. Two forms:

- `env:NAME` reads the environment variable `NAME` at fetch time. Works on every platform.
- `keychain:SERVICE` reads a generic password from the macOS login keychain via
  `security find-generic-password -s SERVICE -w`. **macOS only**: on other platforms a
  `keychain:` ref fails to resolve.

Resolved values are held in memory only: never written to a run directory, and never placed in
the agent's environment. Every `env:` variable named anywhere in `sources.*` — `token`, the
built-in adapters' `apiToken`, `pat`, `apiKey` and `oauthToken`, and all three parts of an `auth`
grant — is stripped from the environment the agent process inherits, so a session that can run
shell commands cannot read them back out. `email` is the one adapter credential field that is not
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

## `permissions.bash` glob semantics

Each entry in `permissions.bash` is a glob pattern matched against the agent's `Bash` tool
command, trimmed of leading and trailing whitespace. A command is allowed if it matches any
pattern in the list; everything else, including every non-`Bash` write tool, is denied.

- `*` matches any run of characters, including spaces and `/`. `git log*` matches
  `git log --oneline -20` and `git log -- some/path`.
  The pattern is a full-string match, not a prefix, so `git log*` also matches
  `git logout`, not just `git log ...`.
- `?` matches exactly one character.
- Matching is case-sensitive.
- The pattern is anchored to the whole command string, not a prefix or substring: `git log`
  without a trailing `*` matches only the exact command `git log`, with no arguments.

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

`provider: claude` (default) or `provider: codex` selects which agent CLI drives runs;
`--provider` on `triage` and `rca` overrides it per invocation.

- `providers.claude.path`: path to the `claude` binary. Empty (the default) looks it up on
  `PATH`.
- `providers.codex.path`: path to the `codex` binary. Empty (the default) looks it up on
  `PATH`.
- `billing: subscription` (default) removes `ANTHROPIC_API_KEY` from the agent's child
  environment so the run authenticates with the CLI's own login and draws on your subscription.
  `billing: api` leaves the key in place, so the run is billed per token against that key
  instead.
